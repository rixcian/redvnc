//go:build windows

package input

import (
	"testing"
	"unsafe"
)

func TestWinInputMatchesMouseInputSize(t *testing.T) {
	if int(unsafe.Sizeof(mouseInput{})) != len((winInput{}).U) {
		t.Fatalf("MOUSEINPUT %d bytes but winInput.U is %d",
			unsafe.Sizeof(mouseInput{}), len((winInput{}).U))
	}
	if unsafe.Sizeof(winInput{}) != winInputSize {
		t.Fatalf("sizeof(winInput)=%d winInputSize=%d", unsafe.Sizeof(winInput{}), winInputSize)
	}
}
