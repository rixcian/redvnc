//go:build windows

package capture

import "fmt"

// DXGICapture captures the screen using the DXGI Desktop Duplication API.
type DXGICapture struct {
	width  uint16
	height uint16
}

func NewScreenCapture() (ScreenCapture, error) {
	return &DXGICapture{}, nil
}

func (d *DXGICapture) Init() error {
	// TODO: Initialize DXGI Desktop Duplication via CGo
	// - Create ID3D11Device
	// - Get IDXGIOutputDuplication
	// - Query output dimensions
	return fmt.Errorf("DXGI capture not yet implemented")
}

func (d *DXGICapture) Bounds() (uint16, uint16) {
	return d.width, d.height
}

func (d *DXGICapture) Capture() ([]byte, int, error) {
	// TODO: AcquireNextFrame, map texture, copy pixels
	return nil, 0, fmt.Errorf("DXGI capture not yet implemented")
}

func (d *DXGICapture) Close() error {
	// TODO: Release DXGI resources
	return nil
}
