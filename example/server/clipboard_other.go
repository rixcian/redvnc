//go:build !windows

package main

import "github.com/rixcian/redvnc/rfb"

// startClipboardSync is a no-op on non-Windows platforms.
// Clipboard monitoring requires OS-specific implementation.
func startClipboardSync(_ *rfb.Server, _ <-chan struct{}) {}
