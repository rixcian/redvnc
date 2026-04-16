//go:build linux && !wayland

package capture

import "errors"

// newWaylandCapture always returns an error in builds that do not have
// the "wayland" build tag. Rebuild with `-tags wayland` (and install
// libpipewire-0.3-dev) to enable the Wayland screen-capture backend.
func newWaylandCapture() (ScreenCapture, error) {
	return nil, errors.New("wayland capture backend not compiled in (build with -tags wayland)")
}
