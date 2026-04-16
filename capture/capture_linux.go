//go:build linux

package capture

import "log/slog"

// NewScreenCapture returns a Linux screen capturer. The concrete
// implementation is selected at runtime based on the active display
// server:
//
//   - On Wayland sessions, a PipeWire-based capturer (via the
//     xdg-desktop-portal ScreenCast API) is returned when the binary was
//     built with the "wayland" build tag. When the tag was not set,
//     the Wayland backend is not compiled in and the code falls back
//     to X11 (which will only succeed if XWayland exposes a usable
//     root window — in practice this is rarely useful, so users on
//     Wayland should build with -tags wayland).
//   - On X11 sessions (or when detection fails), the X11/XShm capturer
//     is returned.
//
// The returned capturer must still have Init() called on it.
func NewScreenCapture() (ScreenCapture, error) {
	if detectDisplayServer() == "wayland" {
		c, err := newWaylandCapture()
		if err == nil {
			return c, nil
		}
		slog.Warn("wayland capture backend unavailable, falling back to X11",
			"error", err,
			"hint", "rebuild with -tags wayland and install libpipewire-0.3-dev")
	}
	return newX11Capture()
}
