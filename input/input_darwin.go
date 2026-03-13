//go:build darwin

package input

import "fmt"

// DarwinInput injects input using macOS CGEvent API.
type DarwinInput struct{}

func NewInputInjector() (InputInjector, error) {
	return &DarwinInput{}, nil
}

func (d *DarwinInput) Init() error {
	// TODO: Check accessibility permissions
	return nil
}

func (d *DarwinInput) KeyEvent(down bool, key uint32) error {
	// TODO: Map X11 keysym → macOS keycode, call CGEventCreateKeyboardEvent
	return fmt.Errorf("macOS input not yet implemented")
}

func (d *DarwinInput) PointerEvent(buttonMask uint8, x, y uint16) error {
	// TODO: Call CGEventCreateMouseEvent
	return fmt.Errorf("macOS input not yet implemented")
}

func (d *DarwinInput) Close() error {
	return nil
}
