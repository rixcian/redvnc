//go:build windows && (amd64 || arm64)

package input

import "unsafe"

// winInput matches the Win32 INPUT layout on 64-bit Windows: DWORD type, padding,
// then the MOUSEINPUT / KEYBDINPUT union (MOUSEINPUT is the largest at 32 bytes).
const winInputSize = uintptr(unsafe.Sizeof(winInput{}))

type winInput struct {
	Type uint32
	_    uint32
	U    [32]byte
}
