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
├── wsproxy/                     # WebSocket-to-TCP VNC proxy
│   ├── server.go                # HTTP server, WebSocket upgrade, config
│   ├── session.go               # Session lifecycle, connection manager
│   ├── proxy.go                 # RFB handshake delegation, bidirectional relay
│   ├── clipboard.go             # Clipboard extension messages (types 129-130)
│   ├── fileupload.go            # File upload protocol (types 131-135)
│   ├── wsproxy_test.go          # Tests
│   └── cmd/main.go              # CLI entry point (redvnc-wsproxy)
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
├── example/                     # Ready-to-run examples
│   ├── server/main.go           # VNC server with animated gradient
│   └── client/main.go           # VNC client with PPM screenshot export
├── web/                         # React/TypeScript browser client
│   ├── src/
│   │   ├── index.ts             # VncClient class and public API
│   │   ├── connection.ts        # WebSocket connection manager
│   │   ├── rfb-parser.ts        # Binary RFB message parser (server→client)
│   │   ├── rfb-writer.ts        # Binary RFB message builder (client→server)
│   │   ├── framebuffer.ts       # Framebuffer state, pixel decoding, dirty tracking
│   │   ├── renderer.ts          # Canvas 2D rendering from framebuffer
│   │   ├── input.ts             # Keyboard/mouse/touch → RFB event translation
│   │   ├── clipboard.ts         # Browser Clipboard API ↔ extension messages
│   │   ├── file-upload.ts       # File upload via extension messages
│   │   ├── types.ts             # Shared TypeScript type definitions
│   │   ├── encodings/           # Framebuffer encoding decoders
│   │   │   ├── raw.ts           # Raw encoding decoder
│   │   │   ├── copyrect.ts      # CopyRect decoder
│   │   │   ├── zlib.ts          # Zlib encoding decoder
│   │   │   └── tight.ts         # Tight encoding decoder (zlib + JPEG)
│   │   └── components/          # React components
│   │       ├── VncViewer.tsx     # Main VNC viewer component
│   │       ├── FileUpload.tsx    # Drag-and-drop file upload UI
│   │       └── Toolbar.tsx       # Clipboard, upload, fullscreen toolbar
│   └── demo/                    # Minimal demo app
│       ├── index.html
│       └── main.tsx
└── capi/                        # C shared library exports
    └── exports.go               # //export functions for P/Invoke
```

## Requirements

- Go 1.24 or later
- Node.js 18+ and npm (for the browser client in `web/`)
- External Go dependencies: `nhooyr.io/websocket` (WebSocket proxy), `github.com/google/uuid` (session IDs)

For building the C shared library (`capi/`), a C compiler is required:
- **Linux:** GCC
- **Windows:** MinGW or MSVC
- **macOS:** Xcode command-line tools

## Prebuilt binaries (GitHub Releases)

Cut a release by pushing a **SemVer tag** from `main` (for example `v1.2.3`). The [Release workflow](.github/workflows/release.yml) runs only for tags matching `v*` and requires the tagged commit to be **reachable from `origin/main`**.

Published assets are **raw executables** (no `.zip` / `.tar.gz`), plus `checksums.txt` (SHA256):

| Asset | Notes |
| --- | --- |
| `redvnc-server_<tag>_<os>_<arch>` | Example VNC server; on Windows the name ends with `.exe` |
| `redvnc-wsproxy_<tag>_<os>_<arch>` | WebSocket proxy; `.exe` on Windows |

Both programs support `-version` (prints the embedded tag). **Linux and macOS** server builds link platform libraries dynamically (X11/Xtst and **x264** on Linux; ScreenCaptureKit/frameworks and **x264** on macOS via Homebrew at build time). Install the corresponding runtime packages on the target machine if the binary fails to start with missing-library errors. The **wsproxy** binaries are built with `CGO_ENABLED=0` and are easy to copy to servers.

## Development Setup

```bash
# Clone the repository
git clone https://github.com/rixcian/redvnc.git
cd redvnc

# Verify everything compiles
go build ./...

# Run the tests
go test ./...

# Run tests with verbose output
go test -v ./rfb/...
```

## Quick Start (macOS)

The `example/` folder contains a working server and client you can run in two terminal windows to see redvnc in action. No external VNC viewer required — but you can also use macOS **Screen Sharing** for a graphical view.

### 1. Start the example server

```bash
# Terminal 1 — starts a VNC server on port 5900 with an animated gradient
go run ./example/server

# With a password:
go run ./example/server -password secret

# Custom resolution:
go run ./example/server -width 1280 -height 720 -port 5901
```

The server generates a shifting colour gradient and logs all keyboard/pointer events received from clients.

### 2. Connect with the example client

```bash
# Terminal 2 — connects, fetches one frame, and prints info
go run ./example/client

# Save a screenshot as PPM (opens natively in macOS Preview):
go run ./example/client -output frame.ppm

# Send test input events to the server:
go run ./example/client -send-input

# With password:
go run ./example/client -password secret

# Open the saved screenshot:
open frame.ppm
```

### 3. Connect with macOS Screen Sharing

macOS has a built-in VNC viewer. While the example server is running:

```bash
# Open Screen Sharing via the "Connect to Server" dialog:
open vnc://localhost:5900
```

Or manually: **Finder > Go > Connect to Server** (Cmd+K) and enter `vnc://localhost:5900`.

You should see the animated gradient pattern. Move your mouse or press keys — the server terminal will log each event.

### All example flags

| Flag | Default | Description |
|------|---------|-------------|
| **Server** | | |
| `-port` | `5900` | TCP port to listen on |
| `-password` | *(none)* | VNC password (empty = no auth) |
| `-width` | `800` | Framebuffer width in pixels |
| `-height` | `600` | Framebuffer height in pixels |
| **Client** | | |
| `-addr` | `127.0.0.1:5900` | Server address to connect to |
| `-password` | *(none)* | VNC password |
| `-output` | *(none)* | Save first frame as PPM image |
| `-send-input` | `false` | Send test key/pointer events |

## Usage

### Starting a VNC Server

```go
package main

import (
    "log"

    "github.com/rixcian/redvnc/rfb"
    "github.com/rixcian/redvnc/rfb/security"
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

    "github.com/rixcian/redvnc/rfb"
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

### Server with TLS Encryption

```go
import "crypto/tls"

// Load or generate a TLS certificate
cert, err := tls.LoadX509KeyPair("server.crt", "server.key")
if err != nil {
    log.Fatal(err)
}

server := rfb.NewServer(rfb.ServerConfig{
    Width:  1024,
    Height: 768,
    Name:   "secure-desktop",
    TLSConfig: &tls.Config{
        Certificates: []tls.Certificate{cert},
    },
})

log.Fatal(server.ListenAndServe(":5900"))
```

### Client with TLS Encryption

```go
import "crypto/tls"

client, err := rfb.Connect("192.168.1.100:5900", rfb.ClientConfig{
    TLSConfig: &tls.Config{
        InsecureSkipVerify: true, // or configure proper CA verification
    },
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

## WebSocket VNC Proxy (`wsproxy`)

The `wsproxy` package provides a WebSocket-to-TCP proxy that bridges browser clients to VNC servers. The proxy performs the full RFB handshake with the VNC server on behalf of the browser, then enters a bidirectional byte relay. Extension messages (clipboard, file upload) are intercepted and handled by the proxy.

```
┌──────────────┐                          ┌──────────────┐                    ┌──────────────┐
│  Browser     │── WebSocket (wss://) ───►│              │── TCP ────────────►│ VNC Server A │
│  Client      │   target=10.0.0.1:5900   │              │                    └──────────────┘
└──────────────┘                          │   wsproxy    │
                                          │   (Go)       │                    ┌──────────────┐
┌──────────────┐                          │              │── TCP ────────────►│ VNC Server B │
│  Browser     │── WebSocket (wss://) ───►│              │                    └──────────────┘
│  Client      │   target=10.0.0.2:5901   │              │
└──────────────┘                          └──────────────┘
```

Each client WebSocket connection creates an independent TCP connection to the target VNC server. Multiple clients can connect to the same or different VNC servers simultaneously.

### Building and Running

```bash
# Build
cd wsproxy && go build -o redvnc-wsproxy ./cmd

# Run as open relay (any VNC target allowed — use only in trusted networks)
./redvnc-wsproxy --listen :8080

# Restrict to specific VNC targets
./redvnc-wsproxy --listen :8080 \
  --allowed-vnc-target 10.0.0.5:5900 \
  --allowed-vnc-target 10.0.0.6:5900

# With TLS
./redvnc-wsproxy --listen :8443 --tls-cert cert.pem --tls-key key.pem \
  --allowed-vnc-target 10.0.0.5:5900

# With connection limits
./redvnc-wsproxy --listen :8080 \
  --max-connections 200 \
  --max-connections-per-target 20

# Custom upload directories
./redvnc-wsproxy --listen :8080 \
  --default-upload-dir /tmp/uploads \
  --allowed-upload-dir /tmp/uploads \
  --allowed-upload-dir /home/user/Documents
```

### CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--listen` | `:8080` | HTTP/WebSocket listen address |
| `--allowed-vnc-target` | *(none)* | Allowed VNC target `host:port` (repeatable; empty = open relay) |
| `--vnc-password` | *(none)* | Default VNC password for all sessions |
| `--max-connections` | `100` | Maximum simultaneous client sessions |
| `--max-connections-per-target` | `10` | Maximum sessions per VNC target |
| `--default-upload-dir` | OS Downloads folder | Default file upload directory |
| `--allowed-upload-dir` | *(none)* | Allowed upload directory (repeatable) |
| `--max-upload-size` | `104857600` (100MB) | Maximum upload file size in bytes |
| `--tls-cert` | *(none)* | TLS certificate file |
| `--tls-key` | *(none)* | TLS key file |
| `--allowed-origin` | *(none)* | Allowed WebSocket Origin header (repeatable) |

### Connection Lifecycle

1. Browser opens `ws(s)://host:port/ws?target=<ip:port>&password=<optional>`
2. Proxy validates the target against the allowlist and checks connection limits
3. Proxy opens a TCP connection to the VNC server and performs the full RFB handshake (version, security, ClientInit/ServerInit)
4. Proxy sends a **SessionInit** extension message (type 128) to the browser with framebuffer dimensions, pixel format, and server name
5. Proxy enters relay mode: standard RFB messages (types 0-6) are forwarded as-is; extension messages (types 128+) are intercepted

### Extension Protocol

Extension messages use types 128-255 and are only exchanged between the browser and the proxy (never forwarded to the VNC server). All messages are binary, big-endian, with a 5-byte envelope: `type(1) + length(4) + payload(length)`.

| Type | Direction | Name | Description |
|------|-----------|------|-------------|
| 128 | Proxy -> Browser | SessionInit | Framebuffer dimensions, pixel format, server name |
| 129 | Browser -> Proxy | ClipboardSet | Set server clipboard (converted to RFB ClientCutText) |
| 130 | Proxy -> Browser | ClipboardUpdate | Server clipboard changed (from RFB ServerCutText) |
| 131 | Browser -> Proxy | UploadBegin | Start chunked file upload |
| 132 | Browser -> Proxy | UploadChunk | File data chunk (max 64KB, with offset) |
| 133 | Browser -> Proxy | UploadEnd | Complete upload with CRC-32 checksum |
| 134 | Proxy -> Browser | UploadStatus | Progress/completion/error response |
| 135 | Browser -> Proxy | UploadCancel | Cancel in-progress upload |

### Programmatic Usage

```go
package main

import (
    "log"

    "github.com/rixcian/redvnc/wsproxy"
)

func main() {
    server := wsproxy.NewServer(wsproxy.Config{
        ListenAddr:              ":8080",
        AllowedVNCTargets:       []string{"10.0.0.5:5900"},
        MaxConnections:          100,
        MaxConnectionsPerTarget: 10,
        DefaultUploadDir:        "/tmp/uploads",
        AllowedUploadDirs:       []string{"/tmp/uploads"},
        MaxUploadSize:           100 * 1024 * 1024,
    })

    log.Fatal(server.ListenAndServe())
}
```

### Security

- **Target allowlisting**: When `AllowedVNCTargets` is set, only listed `host:port` pairs are permitted (HTTP 403 otherwise). Empty = open relay.
- **Connection limits**: `MaxConnections` / `MaxConnectionsPerTarget` prevent resource exhaustion.
- **Origin checking**: `AllowedOrigins` validates the WebSocket `Origin` header.
- **Upload safety**: Filename sanitization (strips path separators, `..`, null bytes), directory authorization (resolved absolute path must be within allowed dirs), size limits, CRC-32 verification, max 4 concurrent uploads per session.
- **Graceful shutdown**: On SIGTERM/SIGINT, sends WebSocket close frames and waits up to 10 seconds for sessions to drain.

## Browser Client (`web`)

The `web/` package is a React/TypeScript library that renders VNC desktops in the browser. It connects to a VNC server through the `wsproxy` WebSocket proxy, decodes framebuffer updates onto a `<canvas>`, and translates browser input events to the RFB protocol.

### Features

- **Framebuffer rendering** — Decodes Raw, CopyRect, Zlib, and Tight (JPEG) encodings into an off-screen `ImageData` buffer with dirty-rect tracking for efficient canvas updates
- **Input handling** — Keyboard events mapped to X11 keysyms, mouse events with button tracking, scroll wheel, and single-touch support
- **Clipboard sync** — Bidirectional clipboard via extension messages and the browser Clipboard API
- **File upload** — Chunked file transfer with CRC-32 verification, progress reporting, and drag-and-drop UI
- **Cursor rendering** — Server-side cursor pseudo-encoding decoded into CSS custom cursors
- **Desktop resize** — Handles dynamic resolution changes via the DesktopSize pseudo-encoding
- **Scale to fit** — Optional scaling of the framebuffer to the canvas size with coordinate translation for input

### Running the Demo

The demo app provides a connection form where you enter the proxy URL, VNC target address, and optional password.

```bash
# 1. Start a VNC server (e.g. the example server)
go run ./example/server

# 2. Start the WebSocket proxy pointing at the VNC server
cd wsproxy && go build -o redvnc-wsproxy ./cmd
./redvnc-wsproxy --listen :8080 --allowed-vnc-target 127.0.0.1:5900

# 3. Start the browser client dev server
cd web
npm install
npm run dev
```

Open the URL printed by Vite (typically `http://localhost:5173`). Enter:

- **WebSocket URL**: `ws://localhost:8080/ws`
- **VNC Target**: `127.0.0.1:5900`
- **Password**: *(leave blank if the server has no auth)*

Click **Connect** to view the remote desktop in your browser.

### Using as a Library

Install the package and use either the framework-agnostic `VncClient` class or the `VncViewer` React component:

```bash
npm install @redvnc/web
```

#### React Component

```tsx
import { VncViewer } from '@redvnc/web';

function App() {
  return (
    <VncViewer
      url="ws://localhost:8080/ws"
      target="192.168.1.50:5900"
      password="secret"
      scaleToFit
      onConnect={() => console.log('Connected')}
      onDisconnect={(reason) => console.log('Disconnected:', reason)}
      onBell={() => console.log('Bell')}
      style={{ width: '100%', height: '100vh' }}
    />
  );
}
```

#### VncClient API

```typescript
import { VncClient } from '@redvnc/web';

const client = new VncClient({
  url: 'ws://localhost:8080/ws',
  target: '192.168.1.50:5900',
  password: 'secret',
  scaleToFit: true,
});

await client.connect();

// Attach to a canvas element
client.attachCanvas(document.getElementById('vnc-canvas') as HTMLCanvasElement);

// Clipboard
client.sendClipboard('Hello from the browser');
client.onClipboard((text) => console.log('Server clipboard:', text));

// File upload
const result = await client.uploadFile(someFile, { dir: '/tmp/uploads' });
client.onUploadProgress((p) => console.log(`${p.percent.toFixed(1)}%`));

// Events
client.on('bell', () => console.log('Bell'));
client.on('resize', (w, h) => console.log(`Resized to ${w}x${h}`));

// Disconnect
client.disconnect();
```

#### VncViewer Props

| Prop | Type | Default | Description |
|------|------|---------|-------------|
| `url` | `string` | — | WebSocket proxy URL (`ws://host:port/ws`) |
| `target` | `string` | — | VNC server address (`host:port`) |
| `password` | `string` | — | VNC authentication password |
| `viewOnly` | `boolean` | `false` | Disable keyboard/mouse input |
| `scaleToFit` | `boolean` | `false` | Scale framebuffer to canvas size |
| `clipboardSync` | `boolean` | `true` | Auto-sync browser clipboard |
| `uploadDir` | `string` | — | Default remote upload directory |
| `onConnect` | `() => void` | — | Called when VNC session is established |
| `onDisconnect` | `(reason: string) => void` | — | Called on disconnect |
| `onBell` | `() => void` | — | Called when server sends a bell |
| `className` | `string` | — | CSS class for the container div |
| `style` | `CSSProperties` | — | Inline styles for the container div |

### Building the Library

```bash
cd web
npm install
npm run build      # Produces dist/index.js, dist/index.cjs, dist/index.d.ts
npm run typecheck   # Type-check without emitting
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
    "github.com/rixcian/redvnc/capture"
    "github.com/rixcian/redvnc/input"
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
- **`wsproxy/`** — Connection manager limits, filename sanitization, unique file paths, upload directory authorization, session lifecycle, end-to-end WebSocket proxy with fake VNC server, target allowlist rejection, missing target validation

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

┌──────────────┐    WebSocket     ┌──────────────┐       TCP        ┌──────────────┐
│  Browser     │◄────────────────►│   wsproxy    │◄────────────────►│  VNC Server  │
│  Client      │  Extension msgs  │  (Go proxy)  │   RFB Protocol   │              │
│  (web/)      │  + RFB relay     └──────────────┘                  └──────────────┘
│  React/TS    │
└──────────────┘
```

The `rfb/` package is pure Go with zero platform dependencies. All platform-specific code is isolated in `capture/` and `input/` behind interfaces, making the protocol layer fully unit-testable on any OS. The `wsproxy/` package reuses `rfb/` types and the `security` package for VNC handshake delegation. The `web/` package is a standalone TypeScript/React library that communicates with `wsproxy` over WebSocket.

## License

See [LICENSE](LICENSE) for details.
