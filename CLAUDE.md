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

## Encoding Pipeline (Server → Client)

This section is here to save re-investigation time. Every encoding has a Go encoder in `rfb/encodings/` and a matching TypeScript decoder in `web/src/encodings/`. The pixel format negotiated during handshake is **32-bit BGRA** (4 bytes/pixel). The server always stores and sends pixels in BGRA order; the browser client reads them accordingly.

### Pixel Format

- **Server internal framebuffer:** BGRA, 4 bytes/pixel, row-major, stride = `width * 4`.
- **Wire format after SetPixelFormat:** same BGRA unless the client requests otherwise.
- **Browser framebuffer (`Framebuffer` class):** `Uint8ClampedArray` in RGBA order (canvas native). The decoders convert B↔R when writing.

### Raw (type 0)

**Server** (`rfb/encodings/encodings.go` — `Raw.Encode`):
- Copies rows of BGRA pixels straight out of the framebuffer slice into `Rectangle.Data`.
- No length prefix. Payload size = `width * height * 4`.

**Browser** (`web/src/encodings/raw.ts` — `decodeRaw`):
- Reads `width * height * 4` bytes from the `DataView` and calls `fb.writeRect`.
- `writeRect` expects BGRA and swaps B↔R before writing to the RGBA canvas buffer.

### CopyRect (type 1)

**Server** (`rfb/encodings/encodings.go` — `CopyRect.EncodeCopyRect`):
- Payload is 4 bytes: `srcX` (uint16 BE) + `srcY` (uint16 BE). No pixel data.
- Note: `CopyRect.Encode` returns an error — always use `EncodeCopyRect`.

**Browser** (`web/src/encodings/copyrect.ts` — `decodeCopyRect`):
- Reads `srcX`/`srcY` from the DataView, then calls `fb.copyRect(srcX, srcY, dstX, dstY, w, h)`.

### Zlib (type 6)

**Server** (`rfb/encodings/encodings.go` — `Zlib.Encode`):
- The `zlib.Writer` is **persistent across calls** (reuses dictionary). Only `buf` is reset per frame.
- Uses `Flush()` (Z_SYNC_FLUSH), never `Close()`, so the stream continues.
- Wire format: **4-byte big-endian length** prefix + zlib-compressed BGRA pixel rows.

**Browser** (`web/src/encodings/zlib.ts` — `ZlibDecoder`):
- Maintains a single persistent `pako.Inflate` instance (must call `init()` first).
- Reads the 4-byte length prefix, passes compressed bytes to `inflate.push(..., Z_SYNC_FLUSH)`.
- Collects output chunks, concatenates, then calls `fb.writeRect` (BGRA → RGBA conversion inside).
- **Critical:** uses `Z_SYNC_FLUSH` (mode `2`) to match server's flush boundary.

### Tight (type 7)

The most complex encoding. Operates on 64×64 pixel tiles.

**Server** (`rfb/encodings/tight.go` — `Tight`):

Each tile goes through a decision tree in `encodeTile`:

| Condition | Sub-encoding | Control byte | Payload |
|---|---|---|---|
| All pixels same color | Solid fill | `0x08` | 3 bytes: R, G, B |
| `tileW*tileH >= 4096` and `colorVariance > 512` | JPEG | `0x09` | compact-length + JPEG data |
| Otherwise | Basic (zlib stream 0) | `streamIdx & 0x03` | compact-length + zlib(RGB) |

- **4 independent zlib streams** (indices 0–3) per `Tight` instance, each persistent.
- Basic sub-encoding strips alpha and sends **RGB** (3 bytes/pixel), not BGRA.
- JPEG path converts BGRA → `image.RGBA`, encodes at configurable quality (default 75).
- **Compact length** encoding (1–3 bytes): values < 128 fit in 1 byte; each byte uses 7 bits + continuation bit.
- `EncodeMulti` returns one `rfb.Rectangle` per tile with correct tile coordinates; `Encode` merges them into a single rectangle for callers that expect one rect per call.

**Browser** (`web/src/encodings/tight.ts` — `TightDecoder`):

- Maintains 4 `pako.Inflate` streams (indices 0–3), must call `init()`.
- Control byte high nibble (bits 7–4): if bit `s+4` is set, stream `s` is reset (new `pako.Inflate`).
- Control byte low nibble (`subType`):
  - `0x08` → Solid: read 3 bytes (R, G, B), call `fb.fillRect`.
  - `0x09` → JPEG: read compact-length, slice bytes, decode via `createImageBitmap(Blob)`, deferred.
  - `0x00–0x03` → Basic: `streamIdx = subType & 0x03`, read compact-length, decompress with that stream, call `fb.writeRectRGB` (RGB → RGBA conversion inside).
- JPEG decodes are **batched** and resolved with `Promise.allSettled` for parallelism. `TightDecoder.decode` is `async`.
- The internal `decompressStream` resets `strm.next_out = 0` before each push — this is a **pako 2.x workaround** to avoid stale output data.

### ZRLE (type 16)

**Server** (`rfb/encodings/zrle.go` — `ZRLE`):
- Persistent zlib stream, flushed (not closed) per rectangle.
- Wire format: **4-byte big-endian length** prefix + zlib-compressed tile stream.
- Pixels are **CPIXEL** (3 bytes: B, G, R — alpha dropped), not BGRA.
- Per-tile sub-encodings written into the zlib stream:

| Subtype | Meaning | Data |
|---|---|---|
| `0` | Raw | 3 bytes × numPixels (CPIXEL) |
| `1` | Solid | 3 bytes (single CPIXEL) |
| `2–16` | Packed palette | palette (3×N bytes) + packed indices (1/2/4 bits per pixel, row-padded to byte) |

Palette selection: if 1 unique color → Solid; 2–16 → PackedPalette; otherwise → Raw. (Server does not implement plain/palette RLE — only the decoder handles those.)

**Browser** (`web/src/encodings/zrle.ts` — `ZrleDecoder`):
- Uses `pako.inflate` (one-shot, not streaming) on the full compressed payload per rectangle.
- Handles all ZRLE subtypes including Plain RLE (`128`) and Palette RLE (`130–255`) that the server doesn't currently emit.
- CPIXEL byte order on the wire: B, G, R. `decodeSolidTile` and `decodeRawTile` read in that order and call `fb.setPixel(x, y, r, g, b, 255)`.

### Framebuffer Write Methods (Browser)

The browser `Framebuffer` class (`web/src/framebuffer.ts`) exposes several write helpers used by decoders:

| Method | Input format | Use case |
|---|---|---|
| `writeRect(x, y, w, h, data)` | BGRA Uint8Array | Raw, Zlib |
| `writeRectRGB(x, y, w, h, data)` | RGB Uint8Array | Tight Basic |
| `fillRect(x, y, w, h, r, g, b)` | individual R/G/B bytes | Tight Solid |
| `setPixel(x, y, r, g, b, a)` | individual bytes | ZRLE per-pixel |
| `drawBitmap(bitmap, x, y, w, h)` | `ImageBitmap` | Tight JPEG |
| `copyRect(srcX, srcY, dstX, dstY, w, h)` | — | CopyRect |

### Key Invariants to Preserve

1. **Zlib streams must not be closed between rectangles** for Zlib and Tight Basic encodings — the decoder maintains decompressor state that depends on the continuous dictionary. Calling `Reset()` on the encoder or creating a new `pako.Inflate` on the decoder will desync the stream.
2. **Tight tile coordinates are absolute** (relative to the full framebuffer), not relative to the rectangle. Both `EncodeMulti` and the browser decoder use absolute `x+tx, y+ty`.
3. **ZRLE CPIXEL is 3 bytes** (B, G, R), not 4. Do not confuse with the Raw encoding's 4-byte BGRA pixels.
4. **Compact length** in Tight is little-endian variable-length: `readCompactLength` in `rfb-parser.ts` and `compactLen` in `tight.go` must stay in sync.
5. **CopyRect has no `Encode` method** — always call `EncodeCopyRect(x, y, w, h, srcX, srcY)` directly.

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
