//go:build windows

package input

import "fmt"

// WindowsInput injects input using the Windows SendInput API.
type WindowsInput struct{}

func NewInputInjector() (InputInjector, error) {
	return &WindowsInput{}, nil
}

func (w *WindowsInput) Init() error {
	// TODO: Initialize via CGo → SendInput
	return nil
}

func (w *WindowsInput) KeyEvent(down bool, key uint32) error {
	// TODO: Map X11 keysym → Windows virtual key code, call SendInput
	return fmt.Errorf("Windows input not yet implemented")
}

func (w *WindowsInput) PointerEvent(buttonMask uint8, x, y uint16) error {
	// TODO: Call SendInput with MOUSEINPUT
	return fmt.Errorf("Windows input not yet implemented")
}

func (w *WindowsInput) Close() error {
	return nil
}
