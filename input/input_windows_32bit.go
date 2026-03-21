//go:build windows && (386 || arm)

package input

import "unsafe"

// winInput matches the Win32 INPUT layout on 32-bit Windows.
const winInputSize = uintptr(unsafe.Sizeof(winInput{}))

type winInput struct {
	Type uint32
	U    [24]byte
}
