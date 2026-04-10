//go:build windows

package capture

import (
	"fmt"
	"log"
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32  = syscall.NewLazyDLL("user32.dll")
	gdi32   = syscall.NewLazyDLL("gdi32.dll")
	shcore  = syscall.NewLazyDLL("shcore.dll")

	procGetDC              = user32.NewProc("GetDC")
	procReleaseDC          = user32.NewProc("ReleaseDC")
	procGetSystemMetrics   = user32.NewProc("GetSystemMetrics")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procBitBlt             = gdi32.NewProc("BitBlt")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")

	procSetProcessDpiAwareness = shcore.NewProc("SetProcessDpiAwareness")
	procSetProcessDPIAware     = user32.NewProc("SetProcessDPIAware")
)

func init() {
	// Enable DPI awareness so GetSystemMetrics returns physical pixels.
	// Try SetProcessDpiAwareness (Win 8.1+) first, fall back to SetProcessDPIAware (Vista+).
	if err := procSetProcessDpiAwareness.Find(); err == nil {
		// PROCESS_PER_MONITOR_DPI_AWARE = 2
		procSetProcessDpiAwareness.Call(2)
	} else {
		procSetProcessDPIAware.Call()
	}
}

const (
	smCXScreen = 0
	smCYScreen = 1
	srcCopy    = 0x00CC0020
	captureBlt = 0x40000000
	biRGB      = 0
	dibRGBColors = 0
)

type bitmapInfoHeader struct {
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

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

// GDICapture captures the primary screen using GDI BitBlt.
// Works on Windows 7 and higher.
type GDICapture struct {
	mu        sync.Mutex
	screenDC  syscall.Handle
	memDC     syscall.Handle
	hBitmap   syscall.Handle
	oldBitmap syscall.Handle
	bits      unsafe.Pointer
	width     uint16
	height    uint16
	pixels    []byte // persistent pixel buffer — allocated once in Init, reused every frame
}

func NewScreenCapture() (ScreenCapture, error) {
	return &DXGICapture{}, nil
}

func (g *GDICapture) Init() error {
	w, _, _ := procGetSystemMetrics.Call(smCXScreen)
	h, _, _ := procGetSystemMetrics.Call(smCYScreen)
	if w == 0 || h == 0 {
		return fmt.Errorf("GetSystemMetrics returned zero screen size")
	}

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return fmt.Errorf("GetDC(NULL) failed")
	}

	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		procReleaseDC.Call(0, screenDC)
		return fmt.Errorf("CreateCompatibleDC failed")
	}

	bmi := bitmapInfo{
		Header: bitmapInfoHeader{
			Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
			Width:       int32(w),
			Height:      -int32(h), // top-down DIB
			Planes:      1,
			BitCount:    32,
			Compression: biRGB,
		},
	}

	var bits unsafe.Pointer
	hBitmap, _, _ := procCreateDIBSection.Call(
		memDC,
		uintptr(unsafe.Pointer(&bmi)),
		dibRGBColors,
		uintptr(unsafe.Pointer(&bits)),
		0, 0,
	)
	if hBitmap == 0 || bits == nil {
		procDeleteDC.Call(memDC)
		procReleaseDC.Call(0, screenDC)
		return fmt.Errorf("CreateDIBSection failed")
	}

	oldBitmap, _, _ := procSelectObject.Call(memDC, hBitmap)

	g.screenDC = syscall.Handle(screenDC)
	g.memDC = syscall.Handle(memDC)
	g.hBitmap = syscall.Handle(hBitmap)
	g.oldBitmap = syscall.Handle(oldBitmap)
	g.bits = bits
	g.width = uint16(w)
	g.height = uint16(h)
	g.pixels = make([]byte, int(w)*int(h)*4)

	log.Println("screen capture: using GDI BitBlt (Windows 7+)")
	return nil
}

func (g *GDICapture) Bounds() (uint16, uint16) {
	return g.width, g.height
}

func (g *GDICapture) Capture() ([]byte, int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.bits == nil {
		return nil, 0, fmt.Errorf("GDI capture not initialized")
	}

	w := int(g.width)
	h := int(g.height)

	ret, _, _ := procBitBlt.Call(
		uintptr(g.memDC), 0, 0, uintptr(w), uintptr(h),
		uintptr(g.screenDC), 0, 0,
		srcCopy|captureBlt,
	)
	if ret == 0 {
		return nil, 0, fmt.Errorf("BitBlt failed")
	}

	stride := w * 4
	size := stride * h

	// Copy pixel data from the DIB section into the persistent buffer.
	// The DIB section stores pixels as BGRX (32bpp, X = unused alpha).
	src := unsafe.Slice((*byte)(g.bits), size)
	copy(g.pixels, src)

	// Set alpha channel to 255 in-place (DIB section leaves it as 0).
	for i := 3; i < size; i += 4 {
		g.pixels[i] = 255
	}

	return g.pixels, stride, nil
}

func (g *GDICapture) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.oldBitmap != 0 {
		procSelectObject.Call(uintptr(g.memDC), uintptr(g.oldBitmap))
		g.oldBitmap = 0
	}
	if g.hBitmap != 0 {
		procDeleteObject.Call(uintptr(g.hBitmap))
		g.hBitmap = 0
	}
	if g.memDC != 0 {
		procDeleteDC.Call(uintptr(g.memDC))
		g.memDC = 0
	}
	if g.screenDC != 0 {
		procReleaseDC.Call(0, uintptr(g.screenDC))
		g.screenDC = 0
	}
	g.bits = nil
	return nil
}
