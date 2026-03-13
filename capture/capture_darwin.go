//go:build darwin

package capture

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>

// captureScreen captures the main display and returns BGRA pixel data.
// The caller must free the returned buffer with free().
static int captureScreen(uint32_t displayID, void **outBuf, int *outWidth, int *outHeight, int *outStride) {
    CGImageRef image = CGDisplayCreateImage(displayID);
    if (!image) {
        return -1;
    }

    size_t w = CGImageGetWidth(image);
    size_t h = CGImageGetHeight(image);

    // Create a BGRA bitmap context.
    size_t stride = w * 4;
    void *buf = malloc(h * stride);
    if (!buf) {
        CGImageRelease(image);
        return -2;
    }

    CGColorSpaceRef cs = CGColorSpaceCreateDeviceRGB();
    CGContextRef ctx = CGBitmapContextCreate(
        buf, w, h, 8, stride,
        cs,
        kCGImageAlphaPremultipliedFirst | kCGBitmapByteOrder32Little // BGRA
    );
    CGColorSpaceRelease(cs);
    if (!ctx) {
        free(buf);
        CGImageRelease(image);
        return -3;
    }

    CGContextDrawImage(ctx, CGRectMake(0, 0, w, h), image);
    CGContextRelease(ctx);
    CGImageRelease(image);

    *outBuf = buf;
    *outWidth = (int)w;
    *outHeight = (int)h;
    *outStride = (int)stride;
    return 0;
}

static void getDisplaySize(uint32_t displayID, int *outWidth, int *outHeight) {
    CGImageRef image = CGDisplayCreateImage(displayID);
    if (image) {
        *outWidth = (int)CGImageGetWidth(image);
        *outHeight = (int)CGImageGetHeight(image);
        CGImageRelease(image);
    } else {
        CGRect bounds = CGDisplayBounds(displayID);
        *outWidth = (int)bounds.size.width;
        *outHeight = (int)bounds.size.height;
    }
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// CGCapture captures the screen using macOS CoreGraphics.
type CGCapture struct {
	displayID C.uint32_t
	width     uint16
	height    uint16
}

func NewScreenCapture() (ScreenCapture, error) {
	return &CGCapture{
		displayID: C.CGMainDisplayID(),
	}, nil
}

func (c *CGCapture) Init() error {
	var w, h C.int
	C.getDisplaySize(c.displayID, &w, &h)
	if w == 0 || h == 0 {
		return fmt.Errorf("failed to get display size (is Screen Recording permission granted?)")
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

	rc := C.captureScreen(c.displayID, &buf, &w, &h, &stride)
	if rc != 0 {
		return nil, 0, fmt.Errorf("captureScreen failed: %d (is Screen Recording permission granted?)", rc)
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
