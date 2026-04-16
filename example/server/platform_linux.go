//go:build linux

package main

import (
	"log"
	"os"
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

// SetClipboard implements rfb.ClipboardSetter for Linux. It tries
// Wayland tools first (`wl-copy`) when running under a Wayland session
// and falls back to X11 tools (`xclip`, `xsel`). The browser sends
// Ctrl+V, which is the correct Linux paste shortcut, so no additional
// key injection is required.
func (a *inputInjectorAdapter) SetClipboard(text string) error {
	candidates := clipboardCandidates()
	for _, args := range candidates {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	// None of the tools succeeded. The browser-sent Ctrl+V will still
	// paste whatever the Linux clipboard currently holds.
	return nil
}

// clipboardCandidates returns the clipboard CLI tools to try in order.
// On Wayland we prefer wl-copy; on X11 we use xclip or xsel.
func clipboardCandidates() [][]string {
	wayland := [][]string{
		{"wl-copy"},
	}
	x11 := [][]string{
		{"xclip", "-sel", "clip"},
		{"xsel", "--clipboard", "--input"},
	}
	if isWaylandSession() {
		return append(wayland, x11...)
	}
	return append(x11, wayland...)
}

func isWaylandSession() bool {
	if os.Getenv("XDG_SESSION_TYPE") == "wayland" {
		return true
	}
	return os.Getenv("WAYLAND_DISPLAY") != ""
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

	if isWaylandSession() {
		log.Println("detected Wayland session; using portal/PipeWire for capture and input (requires -tags wayland build)")
	} else {
		log.Println("detected X11 session; using X11 screen capture and input injection")
	}
	w, h := cap.Bounds()
	log.Printf("display resolution: %dx%d", w, h)

	return cap, &inputInjectorAdapter{injector: inj}, nil, nil
}
