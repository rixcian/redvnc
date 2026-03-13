# redvnc

A cross-platform VNC server and client library written in pure Go, implementing the RFB (Remote Framebuffer) protocol as defined in [RFC 6143](https://datatracker.ietf.org/doc/html/rfc6143).

Built for [Redamp.io](https://redamp.io) — designed to be embedded into agents via C shared library exports and called from .NET (C# P/Invoke).

## Project Structure

```
redvnc/
├── rfb/                         # Core RFB protocol (pure Go, no CGo)
│   ├── protocol.go              # Wire types, constants, message readers/writers
│   ├── server.go                # Multi-client VNC server
│   ├── client.go                # VNC client
│   ├── encodings/               # Framebuffer encodings
│   │   └── encodings.go         # Raw, CopyRect, Zlib
│   └── security/                # Authentication handlers
│       └── security.go          # None, VNC Authentication (DES)
├── capture/                     # Screen capture abstraction
│   ├── capture.go               # ScreenCapture interface
│   ├── capture_linux.go         # X11/XShm (stub)
│   ├── capture_windows.go       # DXGI Desktop Duplication (stub)
│   └── capture_darwin.go        # ScreenCaptureKit (stub)
├── input/                       # Input injection abstraction
│   ├── input.go                 # InputInjector interface
│   ├── input_linux.go           # XTest (stub)
│   ├── input_windows.go         # SendInput (stub)
│   └── input_darwin.go          # CGEvent (stub)
└── capi/                        # C shared library exports
    └── exports.go               # //export functions for P/Invoke
```

## Requirements

- Go 1.24 or later
- No external dependencies — uses only the Go standard library

For building the C shared library (`capi/`), a C compiler is required:
- **Linux:** GCC
- **Windows:** MinGW or MSVC
- **macOS:** Xcode command-line tools

## Development Setup

```bash
# Clone the repository
git clone https://github.com/redamp-io/redvnc.git
cd redvnc

# Verify everything compiles
go build ./...

# Run the tests
go test ./...

# Run tests with verbose output
go test -v ./rfb/...
```

## Usage

### Starting a VNC Server

```go
package main

import (
    "log"

    "github.com/redamp-io/redvnc/rfb"
    "github.com/redamp-io/redvnc/rfb/security"
)

func main() {
    server := rfb.NewServer(rfb.ServerConfig{
        Width:  1024,
        Height: 768,
        Name:   "my-desktop",
    })

    log.Fatal(server.ListenAndServe(":5900"))
}
```

### Starting a Server with VNC Authentication

```go
server := rfb.NewServer(rfb.ServerConfig{
    Width:  1920,
    Height: 1080,
    Name:   "secure-desktop",
    Security: []rfb.SecurityHandler{
        &security.VNCAuth{Password: "secret"},
    },
})

log.Fatal(server.ListenAndServe(":5900"))
```

### Starting a Server with Screen Capture and Input

```go
// Implement the ScreenCapturer interface
type myCapture struct{}

func (m *myCapture) Bounds() (uint16, uint16) {
    return 1920, 1080
}

func (m *myCapture) Capture() ([]byte, int, error) {
    // Return raw BGRA pixel data and stride (bytes per row)
    stride := 1920 * 4
    pixels := make([]byte, 1080*stride)
    // ... fill pixels from your screen capture source ...
    return pixels, stride, nil
}

// Implement the InputHandler interface
type myInput struct{}

func (m *myInput) KeyEvent(down bool, key uint32) {
    // key is an X11 keysym value
    log.Printf("key=%#x down=%v", key, down)
}

func (m *myInput) PointerEvent(buttonMask uint8, x, y uint16) {
    log.Printf("pointer x=%d y=%d buttons=%d", x, y, buttonMask)
}

// Use them
server := rfb.NewServer(rfb.ServerConfig{
    Name:     "live-desktop",
    Capturer: &myCapture{},
    Input:    &myInput{},
})
```

### Connecting as a VNC Client

```go
package main

import (
    "fmt"
    "log"

    "github.com/redamp-io/redvnc/rfb"
)

func main() {
    client, err := rfb.Connect("127.0.0.1:5900", rfb.ClientConfig{
        Shared: true,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    fmt.Printf("Connected: %s (%dx%d)\n", client.Name, client.Width, client.Height)

    // Request a full framebuffer update
    err = client.RequestFramebufferUpdate(false, 0, 0, client.Width, client.Height)
    if err != nil {
        log.Fatal(err)
    }

    // Read the server's response
    msgType, msg, err := client.ReadMessage()
    if err != nil {
        log.Fatal(err)
    }

    if msgType == rfb.MsgFramebufferUpdate {
        update := msg.(*rfb.FramebufferUpdate)
        fmt.Printf("Received %d rectangles\n", len(update.Rects))
    }
}
```

### Client with VNC Authentication

```go
client, err := rfb.Connect("192.168.1.100:5900", rfb.ClientConfig{
    Password:  "secret",
    Shared:    true,
    Encodings: []int32{rfb.EncodingZlib, rfb.EncodingRaw},
})
```

### Sending Input Events

```go
// Send a key press and release (Enter key, X11 keysym 0xff0d)
client.SendKeyEvent(true, 0xff0d)   // key down
client.SendKeyEvent(false, 0xff0d)  // key up

// Move pointer and click (left button = mask 1)
client.SendPointerEvent(0, 500, 300) // move to (500, 300)
client.SendPointerEvent(1, 500, 300) // left button down
client.SendPointerEvent(0, 500, 300) // left button up
```

## Building the C Shared Library

The `capi/` package exports functions for use from C, C#, or any language supporting C FFI.

```bash
# Linux
go build -buildmode=c-shared -o redvnc.so ./capi

# Windows (requires MinGW or MSVC)
go build -buildmode=c-shared -o redvnc.dll ./capi

# macOS
go build -buildmode=c-shared -o redvnc.dylib ./capi
```

This produces a shared library and a header file (`redvnc.h`).

### Exported C Functions

```c
// Start a VNC server on the given port. Pass NULL for no authentication.
// Returns 0 on success, -1 if a server is already running.
int RedVNC_StartServer(int port, const char* password);

// Stop the running VNC server and disconnect all clients.
void RedVNC_StopServer(void);

// Free a string allocated by the library.
void RedVNC_FreeString(char* s);
```

### C# P/Invoke Example

```csharp
using System.Runtime.InteropServices;

public static class RedVNC
{
    [DllImport("redvnc", CallingConvention = CallingConvention.Cdecl)]
    public static extern int RedVNC_StartServer(int port, string? password);

    [DllImport("redvnc", CallingConvention = CallingConvention.Cdecl)]
    public static extern void RedVNC_StopServer();
}

// Usage
RedVNC.RedVNC_StartServer(5900, "secret");
// ... later ...
RedVNC.RedVNC_StopServer();
```

## Encodings

| Encoding | Type ID | Description |
|----------|---------|-------------|
| Raw      | 0       | Uncompressed pixel data. Simple, always supported. |
| CopyRect | 1      | Tells the client to copy pixels from another screen region. |
| Zlib     | 6       | Zlib-compressed pixel data. Good compression ratio. |

The encoding layer is extensible — implement the `encodings.Encoder` interface:

```go
type Encoder interface {
    Encode(x, y, width, height uint16, pixels []byte, stride int) (*rfb.Rectangle, error)
    Type() int32
}
```

## Security

| Type | ID | Description |
|------|----|-------------|
| None | 1  | No authentication. |
| VNC Authentication | 2 | DES challenge-response using a shared password (max 8 characters). |

Implement the `SecurityHandler` interface to add custom authentication:

```go
type SecurityHandler interface {
    Type() uint8
    Handshake(rw io.ReadWriter) error
}
```

## Platform Capture & Input

The `capture/` and `input/` packages define interfaces for screen capture and input injection. Platform-specific implementations use Go build tags.

| Platform | Capture Backend | Input Backend | Status |
|----------|----------------|---------------|--------|
| Linux    | X11 / XShm     | XTest         | Stub   |
| Windows  | DXGI Desktop Duplication | SendInput | Stub |
| macOS    | ScreenCaptureKit | CGEvent     | Stub   |

To create a platform-specific capturer or injector:

```go
import (
    "github.com/redamp-io/redvnc/capture"
    "github.com/redamp-io/redvnc/input"
)

cap, err := capture.NewScreenCapture()
cap.Init()

inp, err := input.NewInputInjector()
inp.Init()
```

### Platform Notes

- **macOS:** Screen Recording permission is required since Mojave. Your app must be granted access via System Preferences > Privacy & Security.
- **Windows:** Session 0 isolation affects services — a helper process in the user session may be needed for screen capture.
- **Windows CGo:** Building the C shared library on Windows requires MinGW or MSVC in your build environment.

## Testing

```bash
# Run all tests
go test ./...

# Run specific package tests
go test -v ./rfb/
go test -v ./rfb/security/
go test -v ./rfb/encodings/

# Run with race detector
go test -race ./...
```

### Test Coverage

- **`rfb/`** — Protocol message encoding/decoding, server handshake, client-server integration, multi-client connections
- **`rfb/security/`** — Bit reversal, DES encryption consistency, None/VNCAuth handshakes, password truncation
- **`rfb/encodings/`** — Raw encoding (full frame + subrectangles), CopyRect, Zlib compression/decompression, encoder reuse

## Architecture

```
┌──────────────┐       TCP        ┌──────────────┐
│  VNC Client  │◄────────────────►│  VNC Server  │
│  (rfb.Client)│   RFB Protocol   │  (rfb.Server)│
└──────────────┘                  └──────┬───────┘
                                         │
                              ┌──────────┼──────────┐
                              │          │          │
                        ┌─────┴────┐ ┌───┴───┐ ┌───┴────┐
                        │ Security │ │Capture│ │ Input  │
                        │ Handler  │ │(DXGI/ │ │(SendIn/│
                        │(None/VNC)│ │ CG/X11)│ │CG/XTest│
                        └──────────┘ └───────┘ └────────┘
```

The `rfb/` package is pure Go with zero platform dependencies. All platform-specific code is isolated in `capture/` and `input/` behind interfaces, making the protocol layer fully unit-testable on any OS.

## License

See [LICENSE](LICENSE) for details.
