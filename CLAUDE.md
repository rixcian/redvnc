# CLAUDE.md — RedVNC Codebase Guide

RedVNC is a cross-platform VNC (Remote Framebuffer / RFC 6143) server and client library written in pure Go, with a TypeScript/React browser client and WebSocket proxy. It is designed to be embedded into agents via C shared library exports (P/Invoke from .NET).

---

## Repository Layout

```
redvnc/
├── rfb/                    # Core RFB protocol (pure Go, zero CGo)
│   ├── server.go           # Multi-client VNC server
│   ├── client.go           # VNC client
│   ├── protocol.go         # Wire types, constants, message I/O
│   ├── encodings/          # Framebuffer encoders (Raw, CopyRect, Zlib, Tight, ZRLE)
│   └── security/           # Auth handlers (None, VNCAuth/DES)
├── wsproxy/                # WebSocket-to-TCP VNC relay
│   ├── server.go           # HTTP server, session manager, health endpoints
│   ├── session.go          # Session lifecycle
│   ├── proxy.go            # RFB handshake delegation, bidirectional relay
│   ├── clipboard.go        # Extension messages 129-130 (clipboard sync)
│   ├── fileupload.go       # Extension messages 131-135 (chunked upload + CRC-32)
│   ├── ratelimit.go        # IP-based auth rate limiting
│   ├── config.go           # Config struct + env var overrides
│   ├── rfbreader.go        # Binary RFB message parsing
│   └── cmd/main.go         # CLI entry point (redvnc-wsproxy)
├── web/                    # React/TypeScript browser client library
│   ├── src/
│   │   ├── index.ts        # VncClient public API, renderer selection
│   │   ├── connection.ts   # WebSocket manager with auto-reconnect
│   │   ├── rfb-parser.ts   # Server→client binary message parsing
│   │   ├── rfb-writer.ts   # Client→server binary message building
│   │   ├── framebuffer.ts  # Pixel state, dirty rect tracking
│   │   ├── renderer.ts     # Canvas 2D renderer
│   │   ├── renderer-webgl.ts # WebGL 2 renderer with fallback
│   │   ├── input.ts        # Keyboard/mouse/touch → RFB events
│   │   ├── clipboard.ts    # Browser Clipboard API ↔ extension messages
│   │   ├── file-upload.ts  # Chunked file upload
│   │   ├── types.ts        # Shared TypeScript types
│   │   ├── components/     # React components (VncViewer, Toolbar, FileUpload, DebugOverlay)
│   │   └── encodings/      # Browser-side decoders (raw, copyrect, zlib, tight, zrle)
│   ├── demo/               # Vite demo application
│   ├── package.json
│   ├── vite.config.ts
│   └── tsconfig.json
├── capture/                # Screen capture interface + platform stubs (Linux/Windows/macOS)
├── input/                  # Input injection interface + platform stubs
├── capi/                   # C shared library exports (//export, P/Invoke)
├── example/
│   ├── server/main.go      # Example VNC server (animated gradient test pattern)
│   └── client/main.go      # Example VNC client (PPM screenshot export)
├── docs/                   # Protocol specs and deployment guides
├── .github/workflows/ci.yml
├── Dockerfile              # Multi-stage: golang:1.24-alpine builder → alpine:3.20 runtime
├── docker-compose.yml
├── go.mod                  # module github.com/rixcian/redvnc, Go 1.24
└── README.md
```

---

## Tech Stack

| Layer | Technology |
|---|---|
| Core protocol | Go 1.24, stdlib only (no CGo in `rfb/`) |
| WebSocket proxy | Go + `nhooyr.io/websocket v1.8.17` |
| Session IDs | `github.com/google/uuid v1.6.0` |
| Browser client | TypeScript 5.3, React 18, Vite 5, Vitest 4 |
| Compression | `pako 2.1.0` (zlib in browser) |
| Container | Docker (Alpine 3.20 runtime) |

---

## Development Commands

### Go

```bash
# Verify compilation
go build ./...

# Run all tests (with race detector on Linux)
go test -race ./...

# Run tests for a specific package
go test -v ./rfb/
go test -v ./rfb/encodings/
go test -v ./rfb/security/
go test -v ./wsproxy/

# Vet
go vet ./...

# Build the proxy binary
go build -o redvnc-wsproxy ./wsproxy/cmd

# Build C shared library
go build -buildmode=c-shared -o redvnc.so ./capi      # Linux
go build -buildmode=c-shared -o redvnc.dll ./capi     # Windows
go build -buildmode=c-shared -o redvnc.dylib ./capi   # macOS
```

### Web (inside `web/`)

```bash
npm install
npm run typecheck   # tsc --noEmit, no emit
npm test            # Vitest suite
npm run build       # Produces dist/index.js, dist/index.cjs, dist/index.d.ts
npm run dev         # Vite dev server (demo app) at http://localhost:5173
```

### Running the Full Stack Locally

```bash
# Terminal 1 — VNC server (animated gradient)
go run ./example/server

# Terminal 2 — WebSocket proxy
go build -o redvnc-wsproxy ./wsproxy/cmd
./redvnc-wsproxy --listen :8080 --allowed-vnc-target 127.0.0.1:5900

# Terminal 3 — Web client dev server
cd web && npm run dev
# Open http://localhost:5173
```

---

## Architecture

```
Browser (React/TS)
    │  WebSocket (ws:// or wss://)
    ▼
WSProxy (Go)        — session management, extension messages, rate limiting
    │  TCP (RFB / RFC 6143)
    ▼
VNC Server (Go)
    │
    ├── ScreenCapturer interface  ←  platform implementations (stubs for now)
    └── InputHandler interface    ←  platform implementations (stubs for now)
```

Extension message types (custom, wsproxy-specific, > 128):
- `129` — Clipboard text server→client
- `130` — Clipboard text client→server
- `131–135` — Chunked file upload with CRC-32 verification

---

## Key Interfaces

```go
// capture/capture.go
type ScreenCapturer interface {
    CaptureScreen() (*image.RGBA, error)
    Close() error
}

// input/input.go
type InputHandler interface {
    KeyEvent(key uint32, down bool) error
    PointerEvent(buttonMask uint8, x, y uint16) error
}

// rfb/encodings/encodings.go
type Encoder interface {
    Encode(rect rfb.Rectangle, fb *rfb.Framebuffer) ([]byte, error)
    Type() rfb.EncodingType
}

// rfb/security/security.go
type SecurityHandler interface {
    Type() uint8
    Authenticate(conn net.Conn) error
}
```

---

## Code Conventions

### Go
- `rfb/` is pure Go with **zero CGo** — keep it that way.
- Platform-specific code lives exclusively in `capture/` and `input/` behind the interfaces above.
- Structured logging via `log/slog` (Go 1.21+). Use `slog.Default()` or pass a `*slog.Logger` via config.
- Error wrapping: `fmt.Errorf("context: %w", err)`.
- Thread safety with `sync.Mutex`; avoid exposing internal mutexes.
- Binary protocol I/O uses `encoding/binary` with `binary.BigEndian` (network byte order).
- Constants for protocol values (message types, encoding types, security types) are in `rfb/protocol.go`.

### TypeScript / React
- Strict TypeScript (`"strict": true`). No `any` without a comment explaining why.
- Binary protocols use `DataView` and `ArrayBuffer` directly.
- React components live in `web/src/components/`. Keep them thin — business logic belongs in the core classes.
- The library exports both ESM (`dist/index.js`) and CJS (`dist/index.cjs`) with type declarations.
- React and React DOM are **peer dependencies** — do not bundle them.

### Tests
- Go test files are co-located with source (`rfb/*_test.go`, `wsproxy/*_test.go`).
- Web test files sit beside the source they test (`*.test.ts`).
- Use the race detector (`go test -race`) on Linux; it is disabled on macOS/Windows in CI due to CGo constraints.
- CI runs on ubuntu-latest, macos-latest, windows-latest via the matrix in `.github/workflows/ci.yml`.

---

## Platform Support Status

| Platform | Screen Capture | Input Injection |
|---|---|---|
| Linux (X11/XShm + XTest) | stub | stub |
| Windows (DXGI + SendInput) | stub | stub |
| macOS (ScreenCaptureKit + CGEvent) | stub | stub |

Stubs compile and satisfy the interfaces but return `errors.New("not implemented")`. Implement them in the appropriate `capture/capture_<os>.go` and `input/input_<os>.go` files.

---

## CI/CD

File: `.github/workflows/ci.yml`

Two jobs:
1. **test** — matrix over ubuntu/macos/windows with Go 1.24. Runs `go build`, `go vet`, and `go test` (with `-race` on Linux only). Uploads coverage artifacts.
2. **web** — ubuntu-only. Runs `npm ci`, `npm run typecheck`, `npm test`.

Linux CI installs `libx11-dev` and `libxtst-dev` for the X11 stubs to compile.

---

## Docker

```bash
# Build image
docker build -t redvnc-wsproxy .

# Run proxy (point at an existing VNC server)
docker run -p 8080:8080 redvnc-wsproxy \
  --listen :8080 \
  --allowed-vnc-target 192.168.1.50:5900

# Full stack with docker-compose
docker compose up
```

Health check: `GET /health` (returns 200 OK when ready).
Readiness check: `GET /ready`.

---

## Common Tasks

### Add a New RFB Encoding

1. Create `rfb/encodings/<name>.go` implementing the `Encoder` interface.
2. Add the encoding type constant to `rfb/protocol.go`.
3. Add client-side decoder to `web/src/encodings/<name>.ts`.
4. Register the encoding in `web/src/rfb-parser.ts` dispatch table.
5. Add tests in both `rfb/encodings/<name>_test.go` and `web/src/encodings/<name>.test.ts`.

### Add a New Extension Message

1. Define the message type constant (128+) in `wsproxy/` (and document in `docs/WEBSOCKET_PROXY_AND_BROWSER_CLIENT_SPEC.md`).
2. Handle it in `wsproxy/proxy.go` (server→browser relay) or a new `wsproxy/<feature>.go`.
3. Implement the browser side in a new `web/src/<feature>.ts`.
4. Expose via `VncClient` in `web/src/index.ts` and update `web/src/types.ts`.

### Implement a Platform Screen Capture / Input

1. Open the stub file: `capture/capture_<os>.go` or `input/input_<os>.go`.
2. Replace the stub body with the real implementation using build-tag-appropriate imports.
3. Add integration tests gated on the platform build tag.

---

## Important Files Quick Reference

| File | Purpose |
|---|---|
| `rfb/server.go` | VNC server: `ServerConfig`, `Server`, client connection loop |
| `rfb/client.go` | VNC client: `ClientConfig`, `Client`, handshake |
| `rfb/protocol.go` | All protocol constants, wire structs, binary I/O helpers |
| `wsproxy/server.go` | Proxy HTTP server, `/health`, `/ready`, session manager |
| `wsproxy/proxy.go` | RFB relay loop, extension message routing |
| `wsproxy/config.go` | `Config` struct, env var overrides |
| `web/src/index.ts` | `VncClient` public API |
| `web/src/rfb-parser.ts` | Server→client message parsing |
| `web/src/rfb-writer.ts` | Client→server message serialization |
| `web/src/components/VncViewer.tsx` | Main React component |
| `capi/exports.go` | C FFI exports for P/Invoke |
| `PLAN.md` | Phased implementation roadmap |
| `docs/WEBSOCKET_PROXY_AND_BROWSER_CLIENT_SPEC.md` | Extension message protocol spec |
