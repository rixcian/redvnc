// Package capture provides a platform-abstracted screen capture interface.
package capture

import "image"

// ScreenCapture captures the screen framebuffer.
type ScreenCapture interface {
	// Init initializes the screen capture subsystem.
	Init() error

	// Bounds returns the screen width and height.
	Bounds() (width, height uint16)

	// Capture captures the current screen.
	// Returns raw pixel data in BGRA format and the stride (bytes per row).
	Capture() (pixels []byte, stride int, err error)

	// Close releases capture resources.
	Close() error
}

// DirtyRectCapture is an optional interface implemented by capturers that can
// report which screen regions changed since the last Capture() call.
//
// After each call to Capture(), the caller may query LastDirtyRects():
//
//   - nil  → the full frame changed (or change extents are unknown); encode everything.
//   - []   → nothing changed since the last frame; encoding can be skipped.
//   - [..] → only the listed rectangles changed; encode selectively.
//
// LastDirtyRects must be called from the same goroutine as Capture() and
// only once per Capture() call. The returned slice is owned by the capturer
// and must not be retained across the next Capture() call.
type DirtyRectCapture interface {
	ScreenCapture
	LastDirtyRects() []image.Rectangle
}
