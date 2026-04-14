//go:build !cgo && !windows

package h264

import "fmt"

func newBackend(width, height int) (h264Backend, error) {
	return nil, fmt.Errorf("not available (built without CGo and not on Windows)")
}
