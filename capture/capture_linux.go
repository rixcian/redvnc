//go:build linux

package capture

import "fmt"

// X11Capture captures the screen using X11/XShm.
type X11Capture struct {
	width  uint16
	height uint16
}

func NewScreenCapture() (ScreenCapture, error) {
	return &X11Capture{}, nil
}

func (x *X11Capture) Init() error {
	// TODO: Connect to X11 display, get root window geometry
	return fmt.Errorf("X11 capture not yet implemented")
}

func (x *X11Capture) Bounds() (uint16, uint16) {
	return x.width, x.height
}

func (x *X11Capture) Capture() ([]byte, int, error) {
	// TODO: XGetImage or XShmGetImage
	return nil, 0, fmt.Errorf("X11 capture not yet implemented")
}

func (x *X11Capture) Close() error {
	return nil
}
