//go:build linux

package input

import "log/slog"

// NewInputInjector returns a Linux input injector. The concrete
// implementation is selected at runtime based on the active display
// server:
//
//   - On Wayland sessions, an injector backed by xdg-desktop-portal
//     RemoteDesktop is returned when the binary was built with the
//     "wayland" build tag.
//   - On X11 sessions (or when detection fails), the XTest injector is
//     returned.
//
// The returned injector must still have Init() called on it.
func NewInputInjector() (InputInjector, error) {
	if detectDisplayServer() == "wayland" {
		i, err := newWaylandInput()
		if err == nil {
			return i, nil
		}
		slog.Warn("wayland input backend unavailable, falling back to X11",
			"error", err,
			"hint", "rebuild with -tags wayland")
	}
	return newX11Input()
}
