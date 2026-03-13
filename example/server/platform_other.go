//go:build !darwin

package main

import (
	"fmt"

	"github.com/redamp-io/redvnc/rfb"
)

func setupPlatformCaptureAndInput() (rfb.ScreenCapturer, rfb.InputHandler, error) {
	return nil, nil, fmt.Errorf("real screen capture not supported on this platform")
}
