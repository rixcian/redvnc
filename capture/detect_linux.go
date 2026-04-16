//go:build linux

package capture

import "os"

// detectDisplayServer returns the active display server for the current
// session. It checks in order:
//
//  1. XDG_SESSION_TYPE — set by the login manager to "wayland", "x11",
//     or "tty". Most reliable on systemd-based distributions.
//  2. WAYLAND_DISPLAY — set when a Wayland compositor is running.
//  3. DISPLAY — set for native X11 and for XWayland. Must be checked last
//     because it is present on most Wayland sessions as well (for XWayland).
//
// Returns one of "wayland", "x11", or "unknown".
func detectDisplayServer() string {
	switch os.Getenv("XDG_SESSION_TYPE") {
	case "wayland":
		return "wayland"
	case "x11":
		return "x11"
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return "wayland"
	}
	if os.Getenv("DISPLAY") != "" {
		return "x11"
	}
	return "unknown"
}
