//go:build darwin

package capture

import "fmt"

// CGCapture captures the screen using macOS CGDisplayStream / ScreenCaptureKit.
type CGCapture struct {
	width  uint16
	height uint16
}

func NewScreenCapture() (ScreenCapture, error) {
	return &CGCapture{}, nil
}

func (c *CGCapture) Init() error {
	// TODO: Initialize CGDisplayStream or ScreenCaptureKit via CGo
	// - Check Screen Recording permission
	// - Get main display bounds
	return fmt.Errorf("macOS capture not yet implemented")
}

func (c *CGCapture) Bounds() (uint16, uint16) {
	return c.width, c.height
}

func (c *CGCapture) Capture() ([]byte, int, error) {
	// TODO: Capture frame via CGDisplayCreateImage or SCStream
	return nil, 0, fmt.Errorf("macOS capture not yet implemented")
}

func (c *CGCapture) Close() error {
	return nil
}
