//go:build darwin

package main

import (
	"log"

	"github.com/rixcian/redvnc/capture"
	"github.com/rixcian/redvnc/input"
	"github.com/rixcian/redvnc/rfb"
)

// inputInjectorAdapter wraps input.InputInjector to implement rfb.InputHandler.
type inputInjectorAdapter struct {
	injector input.InputInjector
}

func (a *inputInjectorAdapter) KeyEvent(down bool, key uint32) {
	if err := a.injector.KeyEvent(down, key); err != nil {
		log.Printf("[input] key event error: %v", err)
	}
}

func (a *inputInjectorAdapter) PointerEvent(buttonMask uint8, x, y uint16) {
	if err := a.injector.PointerEvent(buttonMask, x, y); err != nil {
		log.Printf("[input] pointer event error: %v", err)
	}
}

func setupPlatformCaptureAndInput() (rfb.ScreenCapturer, rfb.InputHandler, error) {
	cap, err := capture.NewScreenCapture()
	if err != nil {
		return nil, nil, err
	}
	if err := cap.Init(); err != nil {
		return nil, nil, err
	}

	inj, err := input.NewInputInjector()
	if err != nil {
		cap.Close()
		return nil, nil, err
	}
	if err := inj.Init(); err != nil {
		cap.Close()
		return nil, nil, err
	}

	log.Println("using macOS screen capture and input injection")
	w, h := cap.Bounds()
	log.Printf("display resolution: %dx%d", w, h)

	return cap, &inputInjectorAdapter{injector: inj}, nil
}
