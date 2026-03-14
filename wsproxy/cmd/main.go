// Command redvnc-wsproxy runs the WebSocket-to-TCP VNC proxy server.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/rixcian/redvnc/wsproxy"
)

type stringSlice []string

func (s *stringSlice) String() string { return fmt.Sprintf("%v", *s) }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	var (
		listen      = flag.String("listen", ":8080", "HTTP/WebSocket listen address")
		tlsCert     = flag.String("tls-cert", "", "TLS certificate file")
		tlsKey      = flag.String("tls-key", "", "TLS key file")
		password    = flag.String("vnc-password", "", "Default VNC password")
		maxConns    = flag.Int("max-connections", 100, "Maximum simultaneous connections")
		maxPerTgt   = flag.Int("max-connections-per-target", 10, "Maximum connections per VNC target")
		defaultDir  = flag.String("default-upload-dir", "", "Default upload directory")
		maxUpload   = flag.Int64("max-upload-size", 100*1024*1024, "Maximum upload file size in bytes")

		allowedTargets stringSlice
		allowedOrigins stringSlice
		allowedUpDirs  stringSlice
	)

	flag.Var(&allowedTargets, "allowed-vnc-target", "Allowed VNC target host:port (repeatable)")
	flag.Var(&allowedOrigins, "allowed-origin", "Allowed WebSocket origin (repeatable)")
	flag.Var(&allowedUpDirs, "allowed-upload-dir", "Allowed upload directory (repeatable)")
	flag.Parse()

	config := wsproxy.Config{
		ListenAddr:              *listen,
		AllowedVNCTargets:       []string(allowedTargets),
		DefaultVNCPassword:      *password,
		MaxConnections:          *maxConns,
		MaxConnectionsPerTarget: *maxPerTgt,
		DefaultUploadDir:        *defaultDir,
		AllowedUploadDirs:       []string(allowedUpDirs),
		MaxUploadSize:           *maxUpload,
		TLSCertFile:             *tlsCert,
		TLSKeyFile:              *tlsKey,
		AllowedOrigins:          []string(allowedOrigins),
	}

	server := wsproxy.NewServer(config)
	if err := server.ListenAndServe(); err != nil {
		log.Printf("server error: %v", err)
		os.Exit(1)
	}
}
