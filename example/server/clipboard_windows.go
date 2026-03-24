//go:build windows

package main

import (
	"fmt"
	"log"
	"syscall"
	"time"
	"unsafe"

	"github.com/rixcian/redvnc/rfb"
)

var (
	clipUser32   = syscall.NewLazyDLL("user32.dll")
	clipKernel32 = syscall.NewLazyDLL("kernel32.dll")

	procOpenClipboard              = clipUser32.NewProc("OpenClipboard")
	procCloseClipboard             = clipUser32.NewProc("CloseClipboard")
	procGetClipboardData           = clipUser32.NewProc("GetClipboardData")
	procGetClipboardSequenceNumber = clipUser32.NewProc("GetClipboardSequenceNumber")
	procGlobalLock                 = clipKernel32.NewProc("GlobalLock")
	procGlobalUnlock               = clipKernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = clipKernel32.NewProc("GlobalSize")
	procRtlMoveMemory              = clipKernel32.NewProc("RtlMoveMemory")
)

const cfUnicodeText = 13

// startClipboardSync polls the Windows clipboard every 500 ms. When the
// content changes it calls server.SendClipboard so all connected VNC clients
// receive a ServerCutText message and the browser clipboard is updated.
func startClipboardSync(server *rfb.Server, stop <-chan struct{}) {
	var lastSeq uint32

	for {
		select {
		case <-stop:
			return
		case <-time.After(500 * time.Millisecond):
		}

		seq, _, _ := procGetClipboardSequenceNumber.Call()
		if uint32(seq) == lastSeq {
			continue
		}
		lastSeq = uint32(seq)

		text, err := readWindowsClipboardText()
		if err != nil {
			log.Printf("[clipboard] read error: %v", err)
			continue
		}
		if text == "" {
			continue
		}

		log.Printf("[clipboard] change detected (%d chars), forwarding to VNC clients", len([]rune(text)))
		server.SendClipboard(text)
	}
}

// readWindowsClipboardText returns the current clipboard text (CF_UNICODETEXT).
// Returns ("", nil) when the clipboard holds no text.
func readWindowsClipboardText() (string, error) {
	r, _, err := procOpenClipboard.Call(0)
	if r == 0 {
		return "", fmt.Errorf("OpenClipboard: %w", err)
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", nil // clipboard holds no text
	}

	size, _, _ := procGlobalSize.Call(h)
	if size == 0 {
		return "", nil
	}

	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return "", fmt.Errorf("GlobalLock failed")
	}
	defer procGlobalUnlock.Call(h)

	// CF_UNICODETEXT is UTF-16LE; size is in bytes so divide by 2 for uint16 count.
	nchars := size / 2
	buf := make([]uint16, nchars)

	// Copy from the HGLOBAL-locked memory into our Go slice via RtlMoveMemory.
	// This avoids converting the GlobalLock uintptr result to unsafe.Pointer
	// directly — instead we pass our Go slice pointer as the destination
	// (safe: converting unsafe.Pointer to uintptr in a syscall argument per
	// Go unsafe rule 4) and use the raw uintptr as the source address.
	procRtlMoveMemory.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		ptr,
		size,
	)

	return syscall.UTF16ToString(buf), nil
}
