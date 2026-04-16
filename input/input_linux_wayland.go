//go:build linux && wayland

// Wayland input injection using xdg-desktop-portal RemoteDesktop.
//
// The primary path calls NotifyKeyboardKeysym so VNC keysyms can be
// forwarded verbatim. If the portal backend lacks that method (older
// xdg-desktop-portal-wlr builds, for example) the code falls back to
// NotifyKeyboardKeycode with a small evdev translation table in
// keysym_evdev.go.
//
// Build with: go build -tags wayland ./...
package input

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/rixcian/redvnc/internal/portal"
)

// WaylandInput injects input via the xdg-desktop-portal RemoteDesktop
// D-Bus interface. A single portal session can be shared with the
// screen-capture code so the user only sees one consent dialog.
type WaylandInput struct {
	session    *portal.Session
	ownSession bool

	streamNode uint32
	streamW    float64
	streamH    float64

	lastButtonMask uint8

	// keysymUnsupported is flipped the first time the portal returns
	// a NotSupported error for NotifyKeyboardKeysym. Subsequent calls
	// go straight to the evdev fallback.
	keysymMu          sync.Mutex
	keysymUnsupported bool
}

// newWaylandInput creates an input injector that owns its portal
// session. It is called by the factory in input_linux.go when the
// "wayland" build tag is enabled.
func newWaylandInput() (InputInjector, error) {
	return &WaylandInput{}, nil
}

// NewWaylandInputWithSession creates an input injector that reuses an
// existing portal session. The injector does not take ownership of
// the session.
func NewWaylandInputWithSession(s *portal.Session) (InputInjector, error) {
	if s == nil {
		return nil, errors.New("wayland input: nil portal session")
	}
	return &WaylandInput{session: s, ownSession: false}, nil
}

func (w *WaylandInput) Init() error {
	if w.session == nil {
		s, err := portal.New(portal.SessionOpts{
			Capture:          true, // required for coordinate-space stream id
			Input:            true,
			RestoreTokenPath: inputDefaultRestoreTokenPath(),
		})
		if err != nil {
			return fmt.Errorf("wayland input: create portal session: %w", err)
		}
		w.session = s
		w.ownSession = true
	}
	streams := w.session.PipeWireStreams()
	if len(streams) > 0 {
		w.streamNode = streams[0].NodeID
		w.streamW = float64(streams[0].Width)
		w.streamH = float64(streams[0].Height)
	}
	return nil
}

// KeyEvent injects a key press or release. key is an X11 keysym.
func (w *WaylandInput) KeyEvent(down bool, key uint32) error {
	if w.session == nil {
		return errors.New("wayland input: not initialized")
	}
	state := portal.KeyStateReleased
	if down {
		state = portal.KeyStatePressed
	}

	w.keysymMu.Lock()
	useKeysym := !w.keysymUnsupported
	w.keysymMu.Unlock()

	if useKeysym {
		err := w.session.NotifyKeyboardKeysym(int32(key), state)
		if err == nil {
			return nil
		}
		if !isNotSupported(err) {
			return err
		}
		w.keysymMu.Lock()
		w.keysymUnsupported = true
		w.keysymMu.Unlock()
		slog.Debug("portal NotifyKeyboardKeysym not supported, falling back to keycode")
	}

	code, ok := keysymToEvdev[key]
	if !ok {
		// Unknown keysym — match X11 behaviour of silently ignoring.
		return nil
	}
	return w.session.NotifyKeyboardKeycode(code, state)
}

// PointerEvent injects pointer motion and button transitions.
func (w *WaylandInput) PointerEvent(buttonMask uint8, x, y uint16) error {
	if w.session == nil {
		return errors.New("wayland input: not initialized")
	}

	if w.streamW > 0 && w.streamH > 0 {
		// The portal NotifyPointerMotionAbsolute coordinate space is
		// documented as "logical pixels relative to the upper left
		// corner of the stream". No normalisation is required.
		if err := w.session.NotifyPointerMotionAbsolute(w.streamNode, float64(x), float64(y)); err != nil {
			return err
		}
	}

	changed := w.lastButtonMask ^ buttonMask
	// VNC bit layout: 0=left, 1=middle, 2=right, 3=scroll up, 4=scroll down.
	buttons := [3]int32{portal.BtnLeft, portal.BtnMiddle, portal.BtnRight}
	for i := uint8(0); i < 3; i++ {
		if changed&(1<<i) != 0 {
			state := portal.ButtonStateReleased
			if buttonMask&(1<<i) != 0 {
				state = portal.ButtonStatePressed
			}
			if err := w.session.NotifyPointerButton(buttons[i], state); err != nil {
				return err
			}
		}
	}
	// Scroll up/down: fire a single discrete axis step on press-down.
	if changed&0x08 != 0 && buttonMask&0x08 != 0 {
		_ = w.session.NotifyPointerAxisDiscrete(portal.AxisVertical, -1)
	}
	if changed&0x10 != 0 && buttonMask&0x10 != 0 {
		_ = w.session.NotifyPointerAxisDiscrete(portal.AxisVertical, 1)
	}

	w.lastButtonMask = buttonMask
	return nil
}

func (w *WaylandInput) Close() error {
	if w.ownSession && w.session != nil {
		_ = w.session.Close()
		w.session = nil
	}
	return nil
}

// isNotSupported checks whether the D-Bus error reports an unknown
// method. godbus wraps these as *dbus.Error with Name containing
// "UnknownMethod" or "NotSupported".
func isNotSupported(err error) bool {
	var derr dbus.Error
	if errors.As(err, &derr) {
		n := derr.Name
		return strings.Contains(n, "UnknownMethod") ||
			strings.Contains(n, "NotSupported") ||
			strings.Contains(n, "NotImplemented")
	}
	return false
}

var inputRestoreTokenPathOnce sync.Once
var inputRestoreTokenPath string

func inputDefaultRestoreTokenPath() string {
	inputRestoreTokenPathOnce.Do(func() {
		dir, err := os.UserConfigDir()
		if err != nil || dir == "" {
			return
		}
		inputRestoreTokenPath = filepath.Join(dir, "redvnc", "portal-token")
	})
	return inputRestoreTokenPath
}
