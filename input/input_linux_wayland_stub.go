//go:build linux && !wayland

package input

import "errors"

// newWaylandInput always returns an error in builds that do not have
// the "wayland" build tag. Rebuild with `-tags wayland` to enable the
// Wayland input-injection backend.
func newWaylandInput() (InputInjector, error) {
	return nil, errors.New("wayland input backend not compiled in (build with -tags wayland)")
}
