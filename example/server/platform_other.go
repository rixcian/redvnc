//go:build !darwin && !linux && !windows

package main

import (
	"fmt"

	"github.com/rixcian/redvnc/rfb"
)

func setupPlatformCaptureAndInput() (rfb.ScreenCapturer, rfb.InputHandler, rfb.CursorProvider, error) {
	return nil, nil, nil, fmt.Errorf("real screen capture not supported on this platform")
}
