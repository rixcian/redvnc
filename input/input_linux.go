//go:build linux

package input

import "fmt"

// X11Input injects input using X11 XTest extension.
type X11Input struct{}

func NewInputInjector() (InputInjector, error) {
	return &X11Input{}, nil
}

func (x *X11Input) Init() error {
	// TODO: Connect to X11 display, check XTest extension
	return nil
}

func (x *X11Input) KeyEvent(down bool, key uint32) error {
	// TODO: XTestFakeKeyEvent
	return fmt.Errorf("X11 input not yet implemented")
}

func (x *X11Input) PointerEvent(buttonMask uint8, xPos, yPos uint16) error {
	// TODO: XTestFakeMotionEvent + XTestFakeButtonEvent
	return fmt.Errorf("X11 input not yet implemented")
}

func (x *X11Input) Close() error {
	return nil
}
