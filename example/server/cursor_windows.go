//go:build windows

package main

import (
	"sync"
	"syscall"
	"unsafe"

	"github.com/rixcian/redvnc/rfb"
)

var (
	curUser32 = syscall.NewLazyDLL("user32.dll")
	curGdi32  = syscall.NewLazyDLL("gdi32.dll")

	procCurGetCursorInfo = curUser32.NewProc("GetCursorInfo")
	procCurGetIconInfo   = curUser32.NewProc("GetIconInfo")
	procCurGetDC         = curUser32.NewProc("GetDC")
	procCurReleaseDC     = curUser32.NewProc("ReleaseDC")
	procCurGetDIBits     = curGdi32.NewProc("GetDIBits")
	procCurGetObjectW    = curGdi32.NewProc("GetObjectW")
	procCurDeleteObject  = curGdi32.NewProc("DeleteObject")
)

// Win32 structures for cursor capture.

type cursorInfo struct {
	CbSize      uint32
	Flags       uint32
	HCursor     syscall.Handle
	PtScreenPos point
}

type point struct {
	X, Y int32
}

type iconInfo struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  syscall.Handle
	HbmColor syscall.Handle
}

type bitmap struct {
	BmType       int32
	BmWidth      int32
	BmHeight     int32
	BmWidthBytes int32
	BmPlanes     uint16
	BmBitsPixel  uint16
	BmBits       uintptr
}

type bitmapInfoHeaderCursor struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// winCursorProvider implements rfb.CursorProvider using Win32 APIs.
type winCursorProvider struct {
	mu         sync.Mutex
	lastHandle syscall.Handle
	lastCursor *rfb.CursorImage
}

func newWinCursorProvider() *winCursorProvider {
	return &winCursorProvider{}
}

func (p *winCursorProvider) Cursor() *rfb.CursorImage {
	p.mu.Lock()
	defer p.mu.Unlock()

	var ci cursorInfo
	ci.CbSize = uint32(unsafe.Sizeof(ci))
	ret, _, _ := procCurGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci)))
	if ret == 0 {
		return nil
	}

	// CURSOR_SHOWING = 1
	if ci.Flags&1 == 0 {
		// Cursor is hidden — send an empty cursor to hide it on the client.
		if p.lastHandle != 0 {
			p.lastHandle = 0
			p.lastCursor = &rfb.CursorImage{}
			return p.lastCursor
		}
		return nil
	}

	// If cursor handle hasn't changed, no update needed.
	if ci.HCursor == p.lastHandle && p.lastCursor != nil {
		return nil
	}

	cursor := captureCursorShape(ci.HCursor)
	if cursor == nil {
		return nil
	}

	p.lastHandle = ci.HCursor
	p.lastCursor = cursor
	return cursor
}

func captureCursorShape(hCursor syscall.Handle) *rfb.CursorImage {
	var ii iconInfo
	ret, _, _ := procCurGetIconInfo.Call(uintptr(hCursor), uintptr(unsafe.Pointer(&ii)))
	if ret == 0 {
		return nil
	}
	// GetIconInfo creates copies of bitmaps that we must free.
	defer func() {
		if ii.HbmColor != 0 {
			procCurDeleteObject.Call(uintptr(ii.HbmColor))
		}
		if ii.HbmMask != 0 {
			procCurDeleteObject.Call(uintptr(ii.HbmMask))
		}
	}()

	// Get mask bitmap dimensions.
	var bm bitmap
	procCurGetObjectW.Call(uintptr(ii.HbmMask), unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm)))
	if bm.BmWidth == 0 || bm.BmHeight == 0 {
		return nil
	}

	w := int(bm.BmWidth)
	h := int(bm.BmHeight)

	// For monochrome cursors, mask is double-height (AND + XOR).
	isColor := ii.HbmColor != 0
	if !isColor {
		h = h / 2
	}

	screenDC, _, _ := procCurGetDC.Call(0)
	if screenDC == 0 {
		return nil
	}
	defer procCurReleaseDC.Call(0, screenDC)

	// Read color pixels (BGRA).
	var pixels []byte
	if isColor {
		pixels = getDIBits32(screenDC, ii.HbmColor, w, h)
		if pixels == nil {
			return nil
		}
	}

	// Read mask as 32bpp so we can easily process it.
	maskPixels := getDIBits32(screenDC, ii.HbmMask, w, int(bm.BmHeight))
	if maskPixels == nil {
		return nil
	}

	if !isColor {
		// Monochrome cursor: AND mask is first half, XOR mask is second half.
		andMask := maskPixels[:w*h*4]
		xorMask := maskPixels[w*h*4:]
		pixels = make([]byte, w*h*4)
		for i := 0; i < w*h; i++ {
			off := i * 4
			andVal := andMask[off] // 0x00 or 0xFF
			xorVal := xorMask[off] // 0x00 or 0xFF
			if andVal == 0 {
				// AND=0: pixel = XOR value
				pixels[off+0] = xorVal // B
				pixels[off+1] = xorVal // G
				pixels[off+2] = xorVal // R
				pixels[off+3] = 255
			} else if xorVal != 0 {
				// AND=1, XOR=1: inverted pixel — show as white
				pixels[off+0] = 255
				pixels[off+1] = 255
				pixels[off+2] = 255
				pixels[off+3] = 255
			}
			// else AND=1, XOR=0: transparent (pixels already zeroed)
		}
	} else {
		// Color cursor: check if alpha channel is used.
		hasAlpha := false
		for i := 3; i < len(pixels); i += 4 {
			if pixels[i] != 0 {
				hasAlpha = true
				break
			}
		}
		if !hasAlpha {
			// Legacy color cursor: use AND mask for opacity.
			// AND mask pixels: 0x00 = opaque, 0xFF = transparent.
			for i := 0; i < w*h; i++ {
				andVal := maskPixels[i*4] // 0x00 or 0xFF
				if andVal == 0 {
					pixels[i*4+3] = 255 // opaque
				} else {
					pixels[i*4+3] = 0 // transparent
				}
			}
		}
	}

	// Build RFB 1-bit mask from alpha channel.
	maskRowBytes := (w + 7) / 8
	mask := make([]byte, maskRowBytes*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			alpha := pixels[(y*w+x)*4+3]
			if alpha > 128 {
				mask[y*maskRowBytes+x/8] |= 0x80 >> uint(x%8)
			}
		}
	}

	return &rfb.CursorImage{
		Width:    uint16(w),
		Height:   uint16(h),
		HotspotX: uint16(ii.XHotspot),
		HotspotY: uint16(ii.YHotspot),
		Pixels:   pixels,
		Mask:     mask,
	}
}

// getDIBits32 reads a bitmap as 32bpp BGRA top-down DIB.
func getDIBits32(hdc uintptr, hBitmap syscall.Handle, w, h int) []byte {
	bmi := bitmapInfoHeaderCursor{
		Size:     uint32(unsafe.Sizeof(bitmapInfoHeaderCursor{})),
		Width:    int32(w),
		Height:   -int32(h), // top-down
		Planes:   1,
		BitCount: 32,
	}
	buf := make([]byte, w*h*4)
	ret, _, _ := procCurGetDIBits.Call(
		hdc,
		uintptr(hBitmap),
		0, uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&bmi)),
		0, // DIB_RGB_COLORS
	)
	if ret == 0 {
		return nil
	}
	return buf
}
