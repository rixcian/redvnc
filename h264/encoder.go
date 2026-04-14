// Package h264 provides an H.264 encoder that implements rfb.MultiEncoder.
// On Linux/macOS it uses x264 via CGo; on Windows it uses Media Foundation
// via syscall. A stub is provided for CGO_ENABLED=0 builds.
package h264

import (
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/rixcian/redvnc/rfb"
)

// h264Backend is the platform-specific H.264 encoding backend.
type h264Backend interface {
	// Encode encodes an NV12 frame and returns H.264 NAL data in Annex B format.
	// Returns nil data (no error) if the encoder has not yet produced output.
	Encode(nv12 []byte, forceIDR bool) (nalData []byte, isKeyframe bool, err error)
	// Close releases encoder resources.
	Close()
}

// H264Encoder encodes BGRA framebuffer pixels to H.264 NAL units.
// It implements rfb.MultiEncoder.
type H264Encoder struct {
	mu      sync.Mutex
	backend h264Backend
	width   int
	height  int

	nv12Buf    []byte // reusable NV12 buffer
	frameCount int64  // for keyframe scheduling
	keyintMax  int64  // IDR every N frames
}

// NewEncoder creates a new H264Encoder for the given dimensions.
// Returns an error if the platform backend is unavailable.
func NewEncoder(width, height int) (*H264Encoder, error) {
	backend, err := newBackend(width, height)
	if err != nil {
		return nil, fmt.Errorf("h264: %w", err)
	}

	nv12Size := width*height + width*height/2 // Y + UV planes
	return &H264Encoder{
		backend:   backend,
		width:     width,
		height:    height,
		nv12Buf:   make([]byte, nv12Size),
		keyintMax: 150, // ~5s at 30fps
	}, nil
}

// Type returns the RFB encoding type for H.264.
func (e *H264Encoder) Type() int32 {
	return rfb.EncodingH264
}

// EncodeMulti encodes the given BGRA pixel region as a single H.264 frame.
// Returns one Rectangle containing the NAL data in the wire format:
// [4 bytes flags][4 bytes NAL length][N bytes NAL data]
func (e *H264Encoder) EncodeMulti(x, y, width, height uint16, pixels []byte, stride int) ([]rfb.Rectangle, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	w := int(width)
	h := int(height)

	// Handle resolution changes.
	if w != e.width || h != e.height {
		e.backend.Close()
		backend, err := newBackend(w, h)
		if err != nil {
			return nil, fmt.Errorf("h264 reinit: %w", err)
		}
		e.backend = backend
		e.width = w
		e.height = h
		nv12Size := w*h + w*h/2
		if len(e.nv12Buf) < nv12Size {
			e.nv12Buf = make([]byte, nv12Size)
		}
		e.nv12Buf = e.nv12Buf[:nv12Size]
		e.frameCount = 0
	}

	// Convert BGRA to NV12.
	bgraToNV12(pixels, w, h, stride, int(x), int(y), e.nv12Buf)

	// Determine if we should force an IDR.
	forceIDR := e.frameCount == 0

	nalData, isKeyframe, err := e.backend.Encode(e.nv12Buf, forceIDR)
	if err != nil {
		return nil, fmt.Errorf("h264 encode: %w", err)
	}
	e.frameCount++

	if nalData == nil {
		// Encoder hasn't produced output yet (latency).
		return nil, nil
	}

	// Build wire format: flags(4) + nalLen(4) + nalData(N)
	var flags uint32
	if isKeyframe {
		flags |= 1
	}

	data := make([]byte, 8+len(nalData))
	binary.BigEndian.PutUint32(data[0:4], flags)
	binary.BigEndian.PutUint32(data[4:8], uint32(len(nalData)))
	copy(data[8:], nalData)

	rect := rfb.Rectangle{
		Header: rfb.RectHeader{
			X:        x,
			Y:        y,
			Width:    width,
			Height:   height,
			Encoding: rfb.EncodingH264,
		},
		Data: data,
	}

	return []rfb.Rectangle{rect}, nil
}

// Reset releases encoder resources.
func (e *H264Encoder) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.backend != nil {
		e.backend.Close()
		e.backend = nil
	}
}

// bgraToNV12 converts BGRA pixels to NV12 (Y plane + interleaved UV plane).
// The source region starts at (srcX, srcY) within the pixel buffer with the given stride.
func bgraToNV12(bgra []byte, w, h, stride, srcX, srcY int, nv12 []byte) {
	yPlane := nv12[:w*h]
	uvPlane := nv12[w*h:]

	for y := 0; y < h; y++ {
		rowOff := (srcY+y)*stride + srcX*4
		for x := 0; x < w; x++ {
			off := rowOff + x*4
			b := int(bgra[off])
			g := int(bgra[off+1])
			r := int(bgra[off+2])

			// BT.601 full-range conversion.
			yVal := ((66*r + 129*g + 25*b + 128) >> 8) + 16
			if yVal > 255 {
				yVal = 255
			} else if yVal < 0 {
				yVal = 0
			}
			yPlane[y*w+x] = byte(yVal)

			if y%2 == 0 && x%2 == 0 {
				uVal := ((-38*r - 74*g + 112*b + 128) >> 8) + 128
				vVal := ((112*r - 94*g - 18*b + 128) >> 8) + 128
				if uVal > 255 {
					uVal = 255
				} else if uVal < 0 {
					uVal = 0
				}
				if vVal > 255 {
					vVal = 255
				} else if vVal < 0 {
					vVal = 0
				}
				uvOff := (y/2)*w + (x/2)*2
				uvPlane[uvOff] = byte(uVal)
				uvPlane[uvOff+1] = byte(vVal)
			}
		}
	}
}
