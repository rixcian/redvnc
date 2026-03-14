// Package capi provides C-exported functions for calling redvnc from C# via P/Invoke.
//
// Build as a shared library:
//
//	go build -buildmode=c-shared -o redvnc.dll ./capi     (Windows)
//	go build -buildmode=c-shared -o redvnc.dylib ./capi   (macOS)
//	go build -buildmode=c-shared -o redvnc.so ./capi      (Linux)
package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"log"
	"sync"
	"unsafe"

	"github.com/rixcian/redvnc/rfb"
	"github.com/rixcian/redvnc/rfb/security"
)

var (
	serverMu sync.Mutex
	server   *rfb.Server
)

//export RedVNC_StartServer
func RedVNC_StartServer(port C.int, password *C.char) C.int {
	serverMu.Lock()
	defer serverMu.Unlock()

	if server != nil {
		return -1 // already running
	}

	config := rfb.ServerConfig{
		Width:  1024,
		Height: 768,
		Name:   "redvnc",
	}

	if password != nil {
		pw := C.GoString(password)
		if pw != "" {
			config.Security = []rfb.SecurityHandler{
				&security.VNCAuth{Password: pw},
			}
		}
	}

	server = rfb.NewServer(config)

	addr := fmt.Sprintf(":%d", int(port))
	go func() {
		if err := server.ListenAndServe(addr); err != nil {
			log.Printf("VNC server error: %v", err)
		}
	}()

	return 0
}

//export RedVNC_StopServer
func RedVNC_StopServer() {
	serverMu.Lock()
	defer serverMu.Unlock()

	if server != nil {
		server.Close()
		server = nil
	}
}

//export RedVNC_FreeString
func RedVNC_FreeString(s *C.char) {
	C.free(unsafe.Pointer(s))
}

func main() {}
