//go:build linux

package main

import (
	"log"
	"os/exec"
	"strings"

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

// SetClipboard implements rfb.ClipboardSetter for Linux.
//
// It sets the X11 CLIPBOARD selection via xclip or xsel (whichever is
// available). The browser sends Ctrl+V which is the correct Linux paste
// shortcut, so no additional key injection is needed.
func (a *inputInjectorAdapter) SetClipboard(text string) error {
	for _, args := range [][]string{
		{"xclip", "-sel", "clip"},
		{"xsel", "--clipboard", "--input"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	// Neither xclip nor xsel is installed. The browser-sent Ctrl+V will still
	// paste whatever the Linux clipboard currently holds.
	return nil
}

func setupPlatformCaptureAndInput() (rfb.ScreenCapturer, rfb.InputHandler, rfb.CursorProvider, error) {
	cap, err := capture.NewScreenCapture()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cap.Init(); err != nil {
		return nil, nil, nil, err
	}

	inj, err := input.NewInputInjector()
	if err != nil {
		cap.Close()
		return nil, nil, nil, err
	}
	if err := inj.Init(); err != nil {
		cap.Close()
		return nil, nil, nil, err
	}

	log.Println("using X11 screen capture and input injection")
	w, h := cap.Bounds()
	log.Printf("display resolution: %dx%d", w, h)

	return cap, &inputInjectorAdapter{injector: inj}, nil, nil
}
