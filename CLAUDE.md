# CLAUDE.md — RedVNC Codebase Guide

RedVNC is a cross-platform VNC (Remote Framebuffer / RFC 6143) server and client library written in pure Go, with a TypeScript/React browser client and WebSocket proxy. It is designed to be embedded into agents via C shared library exports (P/Invoke from .NET).

---

## Repository Layout

```
redvnc/
├── rfb/                    # Core RFB protocol (pure Go, zero CGo)
│   ├── server.go           # Multi-client VNC server + frame timing diagnostics
│   ├── client.go           # VNC client
│   ├── protocol.go         # Wire types, constants, message I/O
│   ├── encodings/          # Framebuffer encoders (Raw, CopyRect, Zlib, Tight, ZRLE)
│   └── security/           # Auth handlers (None, VNCAuth/DES)
├── wsproxy/                # WebSocket-to-TCP VNC relay
│   ├── server.go           # HTTP server, session manager, health endpoints
│   ├── session.go          # Session lifecycle
│   ├── proxy.go            # RFB handshake delegation, raw byte relay
│   ├── clipboard.go        # Extension messages 129-130 (clipboard sync)
│   ├── fileupload.go       # Extension messages 131-135 (chunked upload + CRC-32)
│   ├── ratelimit.go        # IP-based auth rate limiting
│   ├── config.go           # Config struct + env var overrides
│   ├── rfbreader.go        # Binary RFB message parsing (used by browser→VNC path only)
│   └── cmd/main.go         # CLI entry point (redvnc-wsproxy)
├── web/                    # React/TypeScript browser client library
│   ├── src/
│   │   ├── index.ts        # VncClient public API, renderer selection, FBU pipeline
│   │   ├── connection.ts   # WebSocket manager with reassembly buffer
│   │   ├── rfb-parser.ts   # Server→client binary message parsing
│   │   ├── rfb-writer.ts   # Client→server binary message building
│   │   ├── framebuffer.ts  # Pixel state, dirty rect tracking, WebGL texture upload
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
├── capture/                # Screen capture interface + platform implementations
│   ├── capture.go          # ScreenCapturer interface
│   ├── capture_windows.go  # Windows DXGI capture
│   ├── capture_darwin.go   # macOS ScreenCaptureKit
│   └── capture_linux.go    # Linux X11/XShm
├── input/                  # Input injection interface + platform implementations
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
| Compression | `fflate` (zlib inflate in browser — replaced pako for ~2x speed) |
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

### Data Flow

```
Browser (React/TS)
    │  WebSocket (ws:// or wss://)
    ▼
WSProxy (Go)        — raw byte relay (VNC→Browser), extension messages, rate limiting
    │  TCP (RFB / RFC 6143)
    ▼
VNC Server (Go)
    │
    ├── ScreenCapturer interface  ←  platform implementations
    └── InputHandler interface    ←  platform implementations
```

### Proxy Relay Model

The VNC→Browser direction uses **raw byte relay**: the proxy reads available TCP bytes and forwards them immediately as WebSocket messages without parsing RFB message boundaries. This eliminates proxy-side tile parsing latency. The browser's reassembly buffer (`connection.ts`) handles message framing via `tryGetMessageLength()`.

The Browser→VNC direction is message-aware: it intercepts `SetPixelFormat` to track pixel format, and routes extension messages (type >= 128) to local handlers.

Extension message types (custom, wsproxy-specific, > 128):
- `128` — SessionInit (proxy→browser, sent once after handshake)
- `129` — ClipboardSet (browser→proxy)
- `130` — ClipboardUpdate (proxy→browser)
- `131–135` — Chunked file upload with CRC-32 verification

### Browser Client Pipeline (FBU Processing)

```
WebSocket onmessage
  → Reassembly buffer (connection.ts) — accumulates raw bytes, dispatches complete RFB messages
  → parseServerMessage (rfb-parser.ts) — parses FBU into rectangles
  → handleFramebufferUpdate (index.ts) — dispatches to encoding decoders
  → TightDecoder / ZlibDecoder / etc. — decompresses and writes to Framebuffer
  → Framebuffer dirty tracking → WebGL texSubImage2D upload → render
```

**Key performance detail:** The FBU request for the next frame is sent **before** decoding the current frame (pipelining). This overlaps server encoding with client decoding.

### Server Frame Pipeline

```
framebufferWriter (server.go)
  → MaxFPS rate limiting (default 30 FPS)
  → capturer.Capture() — platform screen capture
  → bestEncoding() → Tight/Zlib/ZRLE/Raw encoder
  → WriteFramebufferUpdate → bw.Flush() → TCP
```

**Frame timing diagnostics:** The `framebufferWriter` logs every 5 seconds with `avg_capture`, `avg_encode`, `avg_send`, and `fps` — use this to identify server-side bottlenecks.

**FBU request channel** (`fbReqCh`, capacity 1): if the writer is busy encoding, new requests are dropped. This prevents request accumulation when the server is slower than the client's request rate.

---

## Performance Optimizations (Current State)

These are the optimizations currently in place. Understanding them prevents re-doing work and helps identify what's left to optimize.

### Client-side

| Optimization | Location | Impact |
|---|---|---|
| **Pipelined FBU requests** | `index.ts` `handleFramebufferUpdate` | Request next frame before decoding current — overlaps server encoding with client decoding. Doubled data rate (1.1→3+ MB/s). |
| **fflate instead of pako** | `encodings/tight.ts`, `zlib.ts`, `zrle.ts` | ~2x faster zlib inflate, 8KB vs 45KB bundle. Uses raw `Inflate` with manual 2-byte zlib header skip on first push per stream. |
| **Direct framebuffer writes** | `framebuffer.ts` | `writeRectRGB()` and `fillRect()` write directly to framebuffer, avoiding intermediate buffer allocations. |
| **Parallel JPEG decode** | `encodings/tight.ts` | JPEG tiles collected into `jpegTasks[]`, resolved via `Promise.allSettled` for parallel `createImageBitmap`. |
| **Reusable OffscreenCanvas** | `framebuffer.ts` | Single `OffscreenCanvas` instance for JPEG→pixel extraction instead of creating new ones per tile. |
| **Incremental WebGL upload** | `framebuffer.ts` | `texSubImage2D` for dirty rects < 30% of screen; full `texImage2D` only for large updates. |
| **Parser skip for last rect** | `rfb-parser.ts` `parseFramebufferUpdate` | Skips `getTightDataLength()` tile scan for the last rectangle in an FBU (no need to compute length when there's no next rect). Most FBUs have a single rect. |
| **Extension message fast path** | `connection.ts` | Extension messages (type >= 128) dispatched directly without reassembly buffer when buffer is empty. |

### Proxy-side

| Optimization | Location | Impact |
|---|---|---|
| **Raw byte relay** | `proxy.go` `relayVNCToBrowser` | Forwards TCP bytes directly as WebSocket messages without parsing RFB message boundaries. Eliminates tile-by-tile Tight parsing in the proxy. |
| **Reusable scratch buffers** | `rfbreader.go` | `[12]byte` rectHdr and `[16KB]byte` copyBuf avoid per-tile heap allocations (used by browser→VNC path). |

### Server-side

| Optimization | Location | Impact |
|---|---|---|
| **MaxFPS rate limiting** | `server.go` `framebufferWriter` | Prevents wasting CPU on faster-than-displayable frame rates. Default: 30 FPS. |
| **Frame timing diagnostics** | `server.go` `framebufferWriter` | Logs fps/capture/encode/send breakdown every 5s for bottleneck identification. |
| **Parallel Tight tile encoding** | `rfb/encodings/tight.go` `EncodeMulti` | JPEG tiles fan out to independent goroutines; Basic tiles round-robin across 4 zlib streams (one goroutine per stream). See benchmark results below. |

### Server-side Tight Encoding Benchmark Results

Measured on Intel Xeon Platinum 8581C @ 2.10GHz, 16 logical cores, Go 1.24, 1920×1080.
Run with: `go test -bench=. -benchtime=5s -benchmem ./rfb/encodings/`

| Tile path | Parallel (`EncodeMulti`) | Sequential (baseline) | Speedup |
|---|---|---|---|
| **JPEG** (all 510 tiles, high-variance) | 11.3 ms/frame | 101.5 ms/frame | **9×** |
| **Basic/zlib** (all 510 tiles, low-variance) | 9.9 ms/frame | 28.3 ms/frame | **2.9×** |
| **Solid** (all 510 tiles, uniform) | 5.8 ms/frame | 5.6 ms/frame | ~1× (inline, no goroutines) |

Projected full-frame FPS at 1920×1080 (assuming avg_capture=45ms, avg_send=2ms from timing logs):

| Content | Before | After |
|---|---|---|
| JPEG-heavy | ~7 FPS (147ms) | ~17 FPS (58ms) |
| Basic-heavy | ~13 FPS (75ms) | ~18 FPS (57ms) |
| Typical mixed (~9-11 FPS measured) | ~10 FPS (102ms) | ~16–17 FPS (~58ms) |
| Capture-limited ceiling (encode→0) | — | ~21 FPS (47ms) |

JPEG speedup scales with available CPU cores (9× on 16 cores). Basic speedup is capped at 4× by the 4-stream Tight protocol limit.

### What was tried and didn't help

| Approach | Result | Why |
|---|---|---|
| **Web Worker for Tight decode** | FPS regression (11→6), grey tile artifacts | postMessage overhead for ~510 tiles/FBU outweighed off-thread benefit. Tile transfer serialization dominated. |
| **fflate over pako** | No measurable FPS change | Decompression was not the bottleneck at 9-11 FPS. Kept for smaller bundle and cleaner API. |

### Remaining bottleneck analysis (at ~9-11 FPS, 1920x1080, Tight encoding)

The client-side decode path takes ~35-45ms per frame (could handle ~22-28 FPS). The gap to 9-11 FPS suggests the **server** (screen capture + Tight encoding) is the bottleneck. Use the frame timing logs to confirm:

```
INFO frame timing fps=9.2 avg_capture=45ms avg_encode=55ms avg_send=2ms avg_frame=102ms
```

**Next optimization targets (in priority order):**
1. **Server capture optimization** — if `avg_capture` is high, optimize the platform-specific screen capture (e.g., DXGI on Windows, ScreenCaptureKit on macOS)
2. **Parallel Tight tile encoding** — the Tight encoder is currently single-threaded (tiles processed sequentially in `EncodeMulti`). Two tiers of parallelism are possible:
   - **JPEG tiles**: fully independent (each allocates its own buffer, no shared state). Can be fanned out to goroutines and results collected in order. Straightforward `sync.WaitGroup` / channel approach.
   - **Basic (zlib) tiles**: stream 0 is currently used for all Basic tiles, and zlib stream state is sequential (each tile's compressed bytes depend on the previous tile's dictionary). However, Tight defines **4 independent streams** (indices 0–3), each with its own `fflate.Inflate` on the browser side. Distributing tiles across all 4 streams (e.g., round-robin by tile index) allows 4-way parallelism — one goroutine per stream, each processing its assigned subset in order. Compression ratio drops slightly (each stream sees ¼ the data) but throughput should increase proportionally to CPU cores up to 4×.
3. **Server Tight encoding optimization** — if `avg_encode` is high, profile the Go Tight encoder (`rfb/encodings/tight.go`); the pixel format conversion loops (BGRA→RGB in `encodeBasic`, BGRA→RGBA in `encodeJPEG`) are additional candidates for SIMD or batch optimization
4. **Incremental/dirty-rect capture** — only capture and encode changed screen regions instead of full screen every frame

---

## Key Interfaces

```go
// capture/capture.go
type ScreenCapturer interface {
    Bounds() (width, height uint16)
    Capture() (pixels []byte, stride int, err error)
}

// input/input.go
type InputHandler interface {
    KeyEvent(key uint32, down bool) error
    PointerEvent(buttonMask uint8, x, y uint16) error
}

// rfb/server.go
type MultiEncoder interface {
    EncodeMulti(x, y, width, height uint16, pixels []byte, stride int) ([]Rectangle, error)
    Type() int32
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
| Linux (X11/XShm + XTest) | implemented | implemented |
| Windows (DXGI + SendInput) | implemented | implemented |
| macOS (ScreenCaptureKit + CGEvent) | implemented | implemented |

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

Every encoding has a Go encoder in `rfb/encodings/` and a matching TypeScript decoder in `web/src/encodings/`. The pixel format negotiated during handshake is **32-bit BGRA** (4 bytes/pixel). The server always stores and sends pixels in BGRA order; the browser client reads them accordingly.

### Pixel Format

- **Server internal framebuffer:** BGRA, 4 bytes/pixel, row-major, stride = `width * 4`.
- **Wire format after SetPixelFormat:** client requests RGBA 32bpp, but Tight always uses 3-byte CPIXEL (RGB).
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
- Maintains a single persistent `fflate.Inflate` instance (must call `init()` first).
- Skips the 2-byte zlib header on the first push, then pushes raw DEFLATE data.
- Collects output chunks via callback, concatenates, then calls `fb.writeRect`.
- **Critical:** The zlib stream state persists across rectangles — never create a new `Inflate` between rects.

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

- Maintains 4 `fflate.Inflate` streams wrapped in `ZlibStream` class (handles zlib header stripping).
- Control byte high nibble (bits 7–4): if bit `s+4` is set, stream `s` is reset (new `ZlibStream`).
- Control byte low nibble (`subType`):
  - `0x08` → Solid: read 3 bytes (R, G, B), call `fb.fillRect`.
  - `0x09` → JPEG: read compact-length, slice bytes, decode via `createImageBitmap(Blob)`, deferred.
  - `0x00–0x03` → Basic: `streamIdx = subType & 0x03`, read compact-length, decompress with that stream, call `fb.writeRectRGB` (RGB → RGBA conversion inside).
- JPEG decodes are **batched** and resolved with `Promise.allSettled` for parallelism. `TightDecoder.decode` is `async`.
- The `ZlibStream` wrapper skips the 2-byte zlib header on first push and collects output via fflate's callback.

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
- Uses `fflate.Inflate` with streaming push (not one-shot) per rectangle.
- Skips 2-byte zlib header on first push, maintains persistent stream state.
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

1. **Zlib streams must not be closed between rectangles** for Zlib and Tight Basic encodings — the decoder maintains decompressor state that depends on the continuous dictionary. Creating a new `fflate.Inflate` (or `ZlibStream` in Tight) resets the dictionary and will desync the stream.
2. **Tight tile coordinates are absolute** (relative to the full framebuffer), not relative to the rectangle. Both `EncodeMulti` and the browser decoder use absolute `x+tx, y+ty`.
3. **ZRLE CPIXEL is 3 bytes** (B, G, R), not 4. Do not confuse with the Raw encoding's 4-byte BGRA pixels.
4. **Compact length** in Tight is little-endian variable-length: `readCompactLength` in `rfb-parser.ts` and `compactLen` in `tight.go` must stay in sync.
5. **CopyRect has no `Encode` method** — always call `EncodeCopyRect(x, y, w, h, srcX, srcY)` directly.
6. **fflate zlib header handling:** fflate's `Inflate` handles raw DEFLATE only. VNC sends zlib-wrapped data (2-byte header on first chunk per stream). The `ZlibStream` wrapper in `tight.ts` and decoders in `zlib.ts`/`zrle.ts` skip these 2 bytes on `firstPush` then push raw DEFLATE thereafter.
7. **Proxy sends raw bytes:** The VNC→Browser relay does NOT guarantee complete RFB messages per WebSocket frame. The client's reassembly buffer in `connection.ts` must handle message framing.

---

## Connection & Reassembly Buffer (Browser)

`connection.ts` manages the WebSocket connection and RFB message reassembly:

- **Fast path:** Extension messages (type >= 128) are sent as complete WebSocket frames by the proxy and dispatched directly without buffering.
- **Slow path:** Standard RFB messages (types 0-3) go through the reassembly buffer since the proxy uses raw byte relay and may fragment them. `tryGetMessageLength()` scans the buffer to detect complete messages.
- **Tight tile scanning in `tryGetMessageLength`:** For Tight encoding, this scans all tiles (~510 for 1920x1080) to determine message boundaries. This is unavoidable when using raw relay but only runs once per complete message.
- **Buffer management:** Grows by 2x when needed, compacts consumed data via `copyWithin`.

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

1. Open the implementation file: `capture/capture_<os>.go` or `input/input_<os>.go`.
2. Replace or update the implementation using build-tag-appropriate imports.
3. Add integration tests gated on the platform build tag.

---

## Important Files Quick Reference

| File | Purpose |
|---|---|
| `rfb/server.go` | VNC server: `ServerConfig`, `Server`, frame timing, `framebufferWriter` loop |
| `rfb/client.go` | VNC client: `ClientConfig`, `Client`, handshake |
| `rfb/protocol.go` | All protocol constants, wire structs, binary I/O helpers |
| `wsproxy/server.go` | Proxy HTTP server, `/health`, `/ready`, session manager |
| `wsproxy/proxy.go` | Raw byte relay loop, extension message routing |
| `wsproxy/rfbreader.go` | RFB message parser (browser→VNC path + scratch buffers) |
| `wsproxy/config.go` | `Config` struct, env var overrides |
| `web/src/index.ts` | `VncClient` public API, FBU pipeline, pipelined requests |
| `web/src/connection.ts` | WebSocket manager, reassembly buffer, fast/slow path dispatch |
| `web/src/rfb-parser.ts` | Server→client message parsing, `tryGetMessageLength`, Tight tile scanning |
| `web/src/rfb-writer.ts` | Client→server message serialization |
| `web/src/framebuffer.ts` | Pixel buffer, dirty tracking, WebGL texture upload |
| `web/src/encodings/tight.ts` | Tight decoder with fflate `ZlibStream`, parallel JPEG |
| `web/src/components/VncViewer.tsx` | Main React component |
| `capi/exports.go` | C FFI exports for P/Invoke |
| `PLAN.md` | Phased implementation roadmap |
| `docs/WEBSOCKET_PROXY_AND_BROWSER_CLIENT_SPEC.md` | Extension message protocol spec |
