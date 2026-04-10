//go:build darwin

package capture

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework ScreenCaptureKit -framework CoreGraphics -framework CoreVideo -framework CoreFoundation -framework CoreMedia -framework Foundation
#include "screencapture_darwin.h"
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// CGCapture captures the screen using macOS ScreenCaptureKit.
//
// Init() queries the primary display once and pre-allocates a persistent pixel
// buffer and a CGColorSpaceRef so that per-frame overhead is minimised (Phase A).
//
// Optionally call StartStream() after Init() to switch to push-based streaming
// (Phase B): SCStream pushes CVPixelBuffer frames directly into the pixel buffer
// without the CGContextDrawImage step, reducing capture latency from ~20ms to
// ~8–12ms and removing the need to issue a new capture request each frame.
type CGCapture struct {
	ctx    *C.sckit_ctx_t
	pixels []byte // Go-side view of ctx->pixelBuf, valid for the lifetime of ctx
	width  uint16
	height uint16
	stride int
}

func NewScreenCapture() (ScreenCapture, error) {
	return &CGCapture{}, nil
}

func (c *CGCapture) Init() error {
	ctx := C.sckit_init()
	if ctx == nil {
		return fmt.Errorf("sckit_init failed: check Screen Recording permission in System Settings")
	}
	c.ctx = ctx
	c.width = uint16(ctx.width)
	c.height = uint16(ctx.height)
	c.stride = int(ctx.stride)
	// Create a Go slice header pointing directly into the C pixel buffer.
	// sckit_capture() writes into the buffer and we return this slice without copying.
	size := c.stride * int(c.height)
	c.pixels = unsafe.Slice((*byte)(ctx.pixelBuf), size)
	return nil
}

// StartStream switches the capturer to Phase B (SCStream) mode.
// maxFPS caps the push rate (pass 0 for the system default, typically 60 FPS).
// Call this after Init() and before the first Capture() call.
func (c *CGCapture) StartStream(maxFPS int) error {
	if c.ctx == nil {
		return fmt.Errorf("CGCapture not initialized")
	}
	rc := C.sckit_stream_start(c.ctx, C.int(maxFPS))
	if rc != 0 {
		return fmt.Errorf("sckit_stream_start failed (rc=%d)", rc)
	}
	return nil
}

func (c *CGCapture) Bounds() (uint16, uint16) {
	return c.width, c.height
}

func (c *CGCapture) Capture() ([]byte, int, error) {
	if c.ctx == nil {
		return nil, 0, fmt.Errorf("CGCapture not initialized")
	}
	rc := C.sckit_capture(c.ctx)
	if rc != 0 {
		return nil, 0, fmt.Errorf("screen capture failed (rc=%d)", rc)
	}
	// Return the slice view into the C buffer — no allocation, no extra copy.
	// The caller (PipelinedCapturer) makes its own copy before the next call,
	// so this is safe.
	return c.pixels, c.stride, nil
}

func (c *CGCapture) Close() error {
	if c.ctx != nil {
		C.sckit_destroy(c.ctx)
		c.ctx = nil
		c.pixels = nil
	}
	return nil
}
