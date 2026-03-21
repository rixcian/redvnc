package rfb

import (
	"fmt"
	"net"
	"time"
)

// checkPortAvailable checks whether the given address is available for listening.
// It dials the address with a short timeout; if a connection is established the
// port is already occupied and an error is returned. Works on all platforms
// (Linux, macOS, Windows) without any OS-specific code.
func checkPortAvailable(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid address %q: %w", addr, err)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	checkAddr := net.JoinHostPort(host, port)
	conn, err := net.DialTimeout("tcp", checkAddr, time.Second)
	if err != nil {
		// Could not connect — nothing is listening on that port.
		return nil
	}
	conn.Close()
	return fmt.Errorf("port %s is already in use by another process", addr)
}
