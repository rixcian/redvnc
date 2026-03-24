//go:build darwin

package main

import (
	"fmt"
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

// SetClipboard implements rfb.ClipboardSetter for macOS.
//
// It sets the system clipboard via pbcopy and then injects a Cmd+V keystroke
// so the focused application pastes immediately. This is necessary because
// the browser always sends Ctrl+V, which is not the macOS paste shortcut.
// The browser-sent Ctrl+V key events will arrive after SetClipboard returns
// and are harmless on macOS (not bound to paste in most apps).
func (a *inputInjectorAdapter) SetClipboard(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pbcopy: %w", err)
	}

	// Inject Cmd+V (MetaLeft + v) to trigger paste in the focused app.
	// We do this here because the browser sends Ctrl+V which macOS ignores.
	_ = a.injector.KeyEvent(true, 0xffe7)  // MetaLeft (Command) down
	_ = a.injector.KeyEvent(true, 0x0076)  // v down
	_ = a.injector.KeyEvent(false, 0x0076) // v up
	_ = a.injector.KeyEvent(false, 0xffe7) // MetaLeft (Command) up
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

	log.Println("using macOS screen capture and input injection")
	w, h := cap.Bounds()
	log.Printf("display resolution: %dx%d", w, h)

	return cap, &inputInjectorAdapter{injector: inj}, nil, nil
}
