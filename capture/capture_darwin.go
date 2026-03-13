//go:build darwin

package capture

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework ScreenCaptureKit -framework CoreGraphics -framework CoreFoundation -framework Foundation
#include "screencapture_darwin.h"
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// CGCapture captures the screen using macOS ScreenCaptureKit.
type CGCapture struct {
	width  uint16
	height uint16
}

func NewScreenCapture() (ScreenCapture, error) {
	return &CGCapture{}, nil
}

func (c *CGCapture) Init() error {
	var w, h C.int
	rc := C.sckit_get_display_size(&w, &h)
	if rc != 0 {
		return fmt.Errorf("failed to get display size (rc=%d, is Screen Recording permission granted?)", rc)
	}
	if w == 0 || h == 0 {
		return fmt.Errorf("display size is 0x0")
	}
	c.width = uint16(w)
	c.height = uint16(h)
	return nil
}

func (c *CGCapture) Bounds() (uint16, uint16) {
	return c.width, c.height
}

func (c *CGCapture) Capture() ([]byte, int, error) {
	var buf unsafe.Pointer
	var w, h, stride C.int

	rc := C.sckit_capture_screen(&buf, &w, &h, &stride)
	if rc != 0 {
		return nil, 0, fmt.Errorf("screen capture failed (rc=%d)", rc)
	}
	defer C.free(buf)

	size := int(h) * int(stride)
	pixels := make([]byte, size)
	copy(pixels, unsafe.Slice((*byte)(buf), size))

	return pixels, int(stride), nil
}

func (c *CGCapture) Close() error {
	return nil
}
