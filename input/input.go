// Package input provides a platform-abstracted input injection interface.
package input

// InputInjector injects keyboard and pointer events into the OS.
type InputInjector interface {
	// Init initializes the input injection subsystem.
	Init() error

	// KeyEvent injects a key press or release.
	// key is an X11 keysym value.
	KeyEvent(down bool, key uint32) error

	// PointerEvent injects a pointer movement and/or button state change.
	PointerEvent(buttonMask uint8, x, y uint16) error

	// Close releases input injection resources.
	Close() error
}
