// Package capture provides a platform-abstracted screen capture interface.
package capture

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
