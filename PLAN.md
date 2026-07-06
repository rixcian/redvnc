# RedVNC — Implementation Plan

A detailed plan of what needs to be built to make RedVNC production-ready, organized by phase.

---

## Phase 1: Critical — Core Functionality & Security Hardening

These items must be completed before any production deployment. Without them the project either doesn't work as a real VNC server or has exploitable security holes.

---

### 1.1 Implement Windows Screen Capture (DXGI Desktop Duplication)

**File:** `capture/capture_windows.go`

Implement the `ScreenCapture` interface using the DXGI Desktop Duplication API (available since Windows 8).

**What to build:**
- Initialize DXGI factory, adapter, and output duplication via Windows API syscalls (no CGo).
- `Capture() (*image.RGBA, error)` — acquire the next desktop frame from `IDXGIOutputDuplication`, copy it into a Go `*image.RGBA`. Handle the `AcquireNextFrame` → `MapDesktopSurface` / `GetFramePointerShape` cycle.
- `Release()` — release the DXGI resources (duplication object, device, factory).
- Handle GPU device loss (`DXGI_ERROR_DEVICE_REMOVED`) by reinitializing the pipeline.
- Handle display mode changes (resolution change) by reinitializing the output duplication.
- Handle access denied errors (e.g., Secure Desktop / UAC prompt) gracefully — return a blank frame or last-known frame instead of crashing.
- Use `golang.org/x/sys/windows` for syscall wrappers. Define DXGI COM interfaces as Go structs with vtable pointers.
- Support selecting which monitor to capture via adapter/output index (prepare for multi-monitor later).

**Testing:**
- Unit test with a mock DXGI interface.
- Manual test: run the example server on Windows and verify the real desktop appears in a VNC client.

---

### 1.2 Implement Windows Input Injection (SendInput)

**File:** `input/input_windows.go`

Implement the `InputInjector` interface using the Win32 `SendInput` API.

**What to build:**
- `KeyEvent(down bool, keysym uint32) error` — convert X11 keysym to Windows virtual key code, then call `SendInput` with `KEYBDINPUT`. Build a keysym→VK mapping table covering:
  - ASCII printable characters (0x20–0x7E)
  - Function keys (F1–F24)
  - Navigation keys (Home, End, Page Up/Down, arrows)
  - Modifier keys (Shift, Ctrl, Alt, Super/Win)
  - Numpad keys
  - Special keys (Escape, Tab, Backspace, Enter, Delete, Insert, Print Screen)
- `PointerEvent(buttonMask uint8, x, y uint16) error` — convert RFB button mask and coordinates to `SendInput` with `MOUSEINPUT`. Map RFB buttons: bit 0 → left click, bit 1 → middle click, bit 2 → right click, bits 3–4 → scroll wheel (up/down). Use `MOUSEEVENTF_ABSOLUTE | MOUSEEVENTF_MOVE` with coordinates normalized to 0–65535 range.
- Handle extended keys (right Ctrl, right Alt, numpad Enter) by setting the `KEYEVENTF_EXTENDEDKEY` flag.
- Handle Unicode characters that don't map to VK codes using `KEYEVENTF_UNICODE` and `SendInput` with the scancode set to the UTF-16 code unit.

**Testing:**
- Unit test the keysym→VK mapping table for correctness.
- Manual test: connect via VNC client and verify keyboard/mouse control works.

---

### 1.3 Implement Linux Screen Capture (X11/XShm)

**File:** `capture/capture_linux.go`

Implement using X11 shared memory extension for zero-copy screen capture.

**What to build:**
- Open X11 display connection (`XOpenDisplay`).
- Create a shared memory segment (`shmget`, `shmat`) and attach it to an `XImage` via `XShmCreateImage`.
- `Capture()` — call `XShmGetImage` on the root window, copy the shared memory segment into `*image.RGBA`. Handle BGRA→RGBA byte swapping based on the `XImage` byte order and bits-per-pixel.
- `Release()` — detach shared memory (`shmdt`, `shmctl`), destroy the XImage, close the display.
- Use build tags `//go:build linux` and import via CGo (`#cgo pkg-config: x11 xext`), or use a pure-Go X11 client library (e.g., `github.com/jezek/xgb`) to avoid CGo.
- Handle display disconnection and X11 errors via `XSetErrorHandler`.

**Testing:**
- Integration test on a CI Linux runner with Xvfb (virtual framebuffer).
- Verify captured image dimensions match `XDisplayWidth`/`XDisplayHeight`.

---

### 1.4 Implement Linux Input Injection (XTest)

**File:** `input/input_linux.go`

**What to build:**
- `KeyEvent(down bool, keysym uint32) error` — call `XTestFakeKeyEvent`. Convert keysym to keycode using `XKeysymToKeycode`.
- `PointerEvent(buttonMask uint8, x, y uint16) error` — call `XTestFakeMotionEvent` for movement, `XTestFakeButtonEvent` for button state changes. Track previous button mask to detect press/release transitions.
- Flush the X11 event queue after each injection (`XFlush`).
- Handle keysym→keycode failures (unmapped keysyms) by using `XkbKeysymToModifiers` or falling back to `XChangeKeyboardMapping` to temporarily map the keysym.

---

### 1.5 Authentication Rate Limiting

**Files:** `rfb/server.go`, `wsproxy/server.go`

**What to build:**
- **IP-based rate limiter** — track failed auth attempts per IP address using a map with timestamps.
  - Data structure: `map[string][]time.Time` (IP → list of failure timestamps within the window).
  - Use a sliding window of 5 minutes.
  - After 5 failed attempts within the window, reject new connections from that IP with a 30-second lockout.
  - After 10 failed attempts, increase lockout to 5 minutes.
  - After 20 failed attempts, lockout for 1 hour.
  - Periodically (every minute) clean up expired entries from the map using a background goroutine.
- **Configuration:**
  - `RateLimitWindow time.Duration` (default: 5m)
  - `RateLimitMaxAttempts int` (default: 5)
  - `RateLimitLockoutDuration time.Duration` (default: 30s)
  - `RateLimitEnabled bool` (default: true)
- **Server-side implementation:**
  - Before starting the RFB handshake, check the rate limiter.
  - If the IP is locked out, close the connection immediately with an error.
  - On successful auth, clear the failure count for that IP.
  - On failed auth, record the failure timestamp.
- **Proxy-side implementation:**
  - Same logic applied at the WebSocket upgrade level.
  - Return HTTP 429 (Too Many Requests) with `Retry-After` header when locked out.
- **Thread safety:** Protect the map with `sync.RWMutex`.

**Testing:**
- Test that connections are accepted under the limit.
- Test that the (N+1)th failure triggers lockout.
- Test that lockout expires after the duration.
- Test that successful auth clears the counter.
- Test concurrent access safety with `-race`.

---

### 1.6 Secure Proxy Defaults

**Files:** `wsproxy/server.go`, `wsproxy/cmd/main.go`

**What to build:**

1. **Require explicit opt-in for open relay:**
   - If `AllowedVNCTargets` is empty and no `--allow-any-target` flag is set, refuse to start. Print an error message: `"error: --allowed-vnc-target is required (use --allow-any-target to explicitly allow connections to any VNC server)"`.
   - Add `--allow-any-target` CLI flag (bool, default false).

2. **TLS warning:**
   - If `TLSCertFile` and `TLSKeyFile` are empty, log a prominent warning at startup: `"WARNING: TLS is not configured. All traffic including passwords and screen content will be transmitted in plaintext."`.
   - Add a `--require-tls` flag. When set, refuse to start without TLS config.

3. **Default Origin checking:**
   - When `AllowedOrigins` is empty, log: `"WARNING: No allowed origins configured. WebSocket connections from any origin will be accepted."`.

4. **Password from env/file:**
   - Add `--vnc-password-file PATH` flag — read the first line of the file as the password.
   - Support `REDVNC_VNC_PASSWORD` environment variable as an alternative to `--vnc-password`.
   - Priority: `--vnc-password` flag > `--vnc-password-file` > `REDVNC_VNC_PASSWORD` env var.

5. **TLS minimum version:**
   - When TLS is configured, set `tls.Config.MinVersion = tls.VersionTLS12`.

---

## Phase 2: High Priority — Reliability, Observability & Testing

These items are needed for a stable, operable production deployment.

---

### 2.1 Structured Logging with `log/slog`

**Files:** All Go source files that use `log.Printf`.

Replace the standard `log` package with Go 1.21+ `log/slog` (zero new dependencies).

**What to build:**

1. **Logger initialization** in `rfb/server.go` and `wsproxy/server.go`:
   ```go
   type ServerConfig struct {
       // ... existing fields ...
       Logger *slog.Logger // If nil, use slog.Default()
   }
   ```
   - Default handler: `slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: configuredLevel})`
   - Add `--log-format` flag to proxy: `text` (default) or `json`.
   - Add `--log-level` flag: `debug`, `info` (default), `warn`, `error`.

2. **Replace all log calls** with structured equivalents:
   - `log.Printf("client connected: %s", addr)` → `s.logger.Info("client connected", "remote_addr", addr)`
   - `log.Printf("handshake failed for %s: %v", addr, err)` → `s.logger.Warn("handshake failed", "remote_addr", addr, "error", err)`
   - Add `session_id` attribute to all proxy session log messages.
   - Add `client_id` or `remote_addr` to all server log messages.

3. **Access logging** in the proxy:
   - Log on session start: `level=info msg="session started" session_id=... remote_addr=... target=... user_agent=...`
   - Log on session end: `level=info msg="session ended" session_id=... duration_ms=... bytes_sent=... bytes_received=...`

---

### 2.2 Prometheus Metrics

**Files:** New file `wsproxy/metrics.go`, modifications to `wsproxy/server.go`, `wsproxy/session.go`, `wsproxy/proxy.go`

**What to build:**

1. **Add dependency:** `github.com/prometheus/client_golang/prometheus`

2. **Define metrics:**
   ```
   redvnc_active_sessions          gauge    — Current active WebSocket sessions
   redvnc_connections_total         counter  — Total connections (labels: target, status=success|auth_fail|error)
   redvnc_session_duration_seconds  histogram — Session duration
   redvnc_bytes_transferred_total   counter  — Bytes transferred (labels: direction=client_to_server|server_to_client)
   redvnc_auth_attempts_total       counter  — Auth attempts (labels: result=success|failure, target)
   redvnc_upload_bytes_total        counter  — File upload bytes
   redvnc_upload_files_total        counter  — File uploads (labels: status=success|failed|cancelled)
   redvnc_rate_limit_rejections     counter  — Rate limiter rejections
   ```

3. **Expose metrics endpoint:**
   - Add `/metrics` HTTP handler to the proxy server using `promhttp.Handler()`.
   - Make the metrics endpoint optional via `--metrics-enabled` flag (default: true).

4. **Instrument code:**
   - Increment `active_sessions` on session start, decrement on end.
   - Observe `session_duration_seconds` on session end.
   - Count bytes in the bidirectional relay loop.
   - Count auth attempts in the handshake delegator.

---

### 2.3 Health Check Endpoints

**Files:** `wsproxy/server.go`

**What to build:**
- `GET /health` — returns HTTP 200 `{"status": "ok"}` if the server is running.
- `GET /ready` — returns HTTP 200 `{"status": "ready", "active_sessions": N}` if the server is accepting connections and below `MaxConnections`. Returns HTTP 503 if the server is shutting down or at capacity.
- Both endpoints should respond in <10ms (no external calls).

---

### 2.4 Graceful Shutdown

**Files:** `wsproxy/server.go`, `wsproxy/cmd/main.go`

**What to build:**
- Catch `SIGTERM` and `SIGINT` signals.
- On signal received:
  1. Set server state to "draining" (health endpoint returns 503, `/ready` returns not ready).
  2. Stop accepting new WebSocket connections (return HTTP 503).
  3. Send a close frame to all active WebSocket sessions.
  4. Wait up to `--shutdown-timeout` (default: 30s) for sessions to disconnect.
  5. Force-close remaining sessions after timeout.
  6. Shut down the HTTP server via `http.Server.Shutdown(ctx)`.
  7. Exit with code 0.
- Log each step of the shutdown process.

---

### 2.5 Browser Client Auto-Reconnect

**File:** `web/src/connection.ts`

**What to build:**
- On WebSocket `onclose` (if not intentionally closed by the user):
  1. Emit a `disconnected` event with the close reason.
  2. Start reconnection attempts with exponential backoff: 1s, 2s, 4s, 8s, 16s, 30s (cap).
  3. Add jitter (±20%) to prevent thundering herd.
  4. Emit `reconnecting` event with attempt number before each retry.
  5. On successful reconnection, emit `reconnected` event.
  6. After `maxReconnectAttempts` (default: 10), emit `reconnect_failed` and stop.
- Configuration via `VncClientOptions`:
  ```typescript
  reconnect?: boolean           // default: true
  maxReconnectAttempts?: number  // default: 10
  reconnectBaseDelay?: number   // default: 1000 (ms)
  reconnectMaxDelay?: number    // default: 30000 (ms)
  ```
- On reconnect, re-send `SetPixelFormat` and `SetEncodings`, then request a full framebuffer update.
- The `VncViewer` React component should show a reconnection overlay with a spinner and attempt count.

---

### 2.6 Expand CI Test Coverage

**File:** `.github/workflows/ci.yml`

**What to change:**
1. Change `go test -race ./rfb/...` to `go test -race ./...` so `wsproxy/` tests run.
2. Add a web client CI job:
   ```yaml
   web-tests:
     runs-on: ubuntu-latest
     defaults:
       run:
         working-directory: web
     steps:
       - uses: actions/checkout@v4
       - uses: actions/setup-node@v4
         with:
           node-version: '20'
       - run: npm ci
       - run: npm run typecheck
       - run: npm test   # After tests are added
   ```
3. Add a code coverage step: `go test -race -coverprofile=coverage.out ./...` and upload to a coverage service.

---

### 2.7 Browser Client Unit Tests

**Files:** New test files in `web/src/`

**What to build:**

Set up Vitest (`npm install -D vitest`) and create tests for:

- `rfb-parser.test.ts` — Test parsing of all server message types (FramebufferUpdate, ServerCutText, Bell, SetColourMapEntry) from known byte sequences. Test handling of truncated messages, oversized messages, and unknown message types.
- `rfb-writer.test.ts` — Test serialization of all client messages. Verify byte-level output against known-good sequences.
- `framebuffer.test.ts` — Test dirty rectangle tracking, framebuffer resize, pixel format application.
- `encodings/raw.test.ts` — Test Raw decoder with known pixel data.
- `encodings/copyrect.test.ts` — Test CopyRect with overlapping and non-overlapping regions.
- `input.test.ts` — Test keysym mapping for common keys, modifier key handling, mouse button mask calculation.
- `connection.test.ts` — Test reconnection logic (mock WebSocket), backoff timing, max attempts.

---

### 2.8 Integration Tests

**File:** New `rfb/integration_test.go`

**What to build:**
- Start an in-process VNC server with a known test pattern (solid color framebuffer).
- Connect an in-process VNC client.
- Request a full framebuffer update.
- Verify the received pixel data matches the expected test pattern.
- Test with both `SecurityNone` and `VNCAuth`.
- Test with TLS enabled (using self-signed cert generated in test setup).
- Test multi-client connections (connect 3 clients, verify all receive updates).
- Test client disconnect handling (connect, disconnect, verify server doesn't crash).

---

## Phase 3: Medium Priority — Performance, Deployment & Versioning

---

### 3.1 ZRLE Encoding

**Files:** New `rfb/encodings/zrle.go`, `web/src/encodings/zrle.ts`

ZRLE (Zlib Run-Length Encoding, type 16) is the most bandwidth-efficient standard VNC encoding.

**Encoding preference (server) — target state**

We want **`bestEncoding` to prefer ZRLE over Zlib** (e.g. `Tight > ZRLE > Zlib > Raw`) for bandwidth on clients that advertise both. That order is **not enabled until ZRLE is fixed** for real-world interoperability.

Before raising ZRLE above Zlib, the server must:

- **Respect the client pixel format** — ZRLE **CPIXEL** bytes must follow the client’s current format (after `SetPixelFormat`), the same way Raw/Zlib use `ConvertPixels`. Encoding only the server’s native BGRA layout breaks clients that negotiate a different format (e.g. RVNC Viewer on iOS).
- **Match the full ZRLE spec in production** — either route framebuffer updates through `rfb/encodings/zrle.go` (packed palette, RLE, etc.) or bring the inlined path in `rfb/server.go` to parity; the minimal solid/raw tile path is not enough for all decoders.
- **One complete zlib stream per ZRLE rectangle** — `zlib.NewWriter` → write tiles → `Close()` for each update; do not reuse a single writer across framebuffer updates.

After the above, restore preference order to **ZRLE before Zlib** and regression-test with third-party clients (mobile VNC viewers, TigerVNC, RealVNC, etc.).

**Server-side (Go):**
- Divide the framebuffer update rectangle into 64×64 pixel tiles.
- For each tile, analyze the pixel data and choose the most efficient sub-encoding:
  - Raw (subtype 0): for complex tiles with many colors.
  - Solid (subtype 1): single-color tiles.
  - Packed palette (subtypes 2–16): tiles with ≤16 distinct colors — encode as palette + packed indices.
  - RLE (subtype 128): run-length encode pixel values.
  - Palette RLE (subtypes 130–255): combine palette with RLE for tiles with few colors and runs.
- Compress the tile data stream with zlib — **one writer + `Close()` per framebuffer rectangle** (each ZRLE blob is a standalone zlib stream; dictionary reuse across rectangles is not part of the RFB ZRLE framing).
- Prefix with a 4-byte length (compressed size).

**Client-side (TypeScript):**
- Decompress the zlib stream.
- Parse tiles from the decompressed data, applying the sub-encoding type to reconstruct pixels.
- Write decoded tiles into the framebuffer.

**Testing:**
- Round-trip test: encode a known image with ZRLE in Go, decode in Go, compare pixels.
- Compression ratio test: verify ZRLE compresses better than Raw for typical screen content.

---

### 3.2 Adaptive Encoding & Quality

**Files:** `rfb/server.go`, `wsproxy/proxy.go`

**What to build:**
- **Bandwidth estimation:** Track bytes sent and time elapsed per framebuffer update in the proxy relay. Compute a rolling average bandwidth (bytes/second) over the last 10 seconds.
- **Automatic quality adjustment** for Tight encoding:
  - High bandwidth (>5 Mbps): JPEG quality 90, full color.
  - Medium bandwidth (1–5 Mbps): JPEG quality 60.
  - Low bandwidth (<1 Mbps): JPEG quality 30, consider grayscale.
- **Frame rate limiting:**
  - Add `MaxFPS int` to server config (default: 30).
  - If the capturer produces frames faster than `MaxFPS`, drop intermediate frames.
  - On low bandwidth, dynamically reduce to 10–15 FPS.
- **Encoding negotiation hints:**
  - If the client supports both Tight and ZRLE, prefer ZRLE for static content and Tight (JPEG) for rapidly changing regions. (Depends on ZRLE correctness and **ZRLE-before-Zlib** preference as described in §3.1.)

---

### 3.3 Configuration File Support

**Files:** `wsproxy/cmd/main.go`, new `wsproxy/config.go`

**What to build:**
- Support a YAML configuration file via `--config PATH` flag.
- Structure:
  ```yaml
  listen: ":8080"
  tls:
    cert: /etc/redvnc/cert.pem
    key: /etc/redvnc/key.pem
    min_version: "1.2"
    require: false
  security:
    allowed_vnc_targets:
      - "10.0.0.1:5900"
      - "10.0.0.2:5900"
    allowed_origins:
      - "https://app.example.com"
    rate_limit:
      enabled: true
      max_attempts: 5
      window: "5m"
      lockout: "30s"
  limits:
    max_connections: 100
    max_connections_per_target: 10
    max_upload_size: "100MB"
  uploads:
    default_dir: "/var/redvnc/uploads"
    allowed_dirs:
      - "/var/redvnc/uploads"
  logging:
    level: "info"
    format: "json"
  ```
- Support environment variable overrides with `REDVNC_` prefix (e.g., `REDVNC_LISTEN=:9090`).
- Priority: CLI flags > env vars > config file > defaults.
- Use `gopkg.in/yaml.v3` (or `encoding/json` if zero-dep is preferred).
- Validate the full config on load and report all errors at once (not one at a time).

---

### 3.4 Dockerfile & Docker Compose

**Files:** New `Dockerfile`, `docker-compose.yml`

**Dockerfile (multi-stage):**
```dockerfile
# Stage 1: Build
FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /redvnc-wsproxy ./wsproxy/cmd

# Stage 2: Runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /redvnc-wsproxy /usr/local/bin/
EXPOSE 8080
ENTRYPOINT ["redvnc-wsproxy"]
```

**docker-compose.yml:**
```yaml
services:
  proxy:
    build: .
    ports:
      - "8080:8080"
    command:
      - "--listen=:8080"
      - "--allowed-vnc-target=vnc-server:5900"
    depends_on:
      - vnc-server
  vnc-server:
    image: redvnc-example-server   # built from example/server
    expose:
      - "5900"
```

---

### 3.5 Semantic Versioning & Changelog

**What to do:**
1. Create `CHANGELOG.md` following Keep a Changelog format.
2. Tag the current state as `v0.1.0`.
3. Document the versioning policy in the README:
   - `v0.x.y` — pre-stable, breaking changes may occur in minor versions.
   - `v1.0.0` — stable API, semver guarantees apply.
4. Add a `Version` variable in `rfb/version.go`:
   ```go
   package rfb
   var Version = "0.1.0"
   ```
   Set it at build time via `-ldflags "-X rfb.Version=$(git describe --tags)"`.
5. Expose the version in the proxy's `/health` endpoint response.

---

## Phase 4: Low Priority — Polish & Advanced Features

---

### 4.1 macOS Platform Support

**Files:** `capture/capture_darwin.go`, `input/input_darwin.go`

**Capture:**
- Use `ScreenCaptureKit` framework (macOS 12.3+) via CGo.
- `SCShareableContent.getWithCompletionHandler` to enumerate displays.
- `SCStream` to capture frames as `CMSampleBuffer`.
- Convert `CVPixelBuffer` (BGRA) to `*image.RGBA`.

**Input:**
- Use `CGEvent` API via CGo.
- `CGEventCreateKeyboardEvent` for key events.
- `CGEventCreateMouseEvent` for pointer events.
- `CGEventPost(kCGHIDEventTap, event)` to inject.

---

### 4.2 Multi-Monitor Support

**Files:** `capture/capture.go` (interface change), platform implementations

**What to build:**
- Add `ListDisplays() ([]Display, error)` to the `ScreenCapture` interface.
  ```go
  type Display struct {
      ID     int
      Name   string
      X, Y   int    // Position in virtual desktop
      Width  int
      Height int
      Scale  float64 // DPI scaling factor
  }
  ```
- Add `SetDisplay(id int) error` to select which display to capture.
- Add `CaptureRegion(x, y, w, h int) (*image.RGBA, error)` for partial capture.
- Server-side: add `DisplayID` to `ServerConfig`.
- Browser client: add display selector UI in toolbar when multiple displays are available.

---

### 4.3 WebGL Renderer

**File:** New `web/src/renderer-webgl.ts`

**What to build:**
- Create a WebGL 2 renderer as an alternative to the Canvas 2D renderer.
- Use a fullscreen quad with a texture that is updated via `texSubImage2D` for dirty rectangles.
- Benefits: GPU-accelerated scaling, compositing, and potential for shader-based color correction.
- Feature-detect WebGL 2 support; fall back to Canvas 2D if unavailable.
- Configuration: `renderer?: 'auto' | 'canvas2d' | 'webgl'` in `VncClientOptions`.

---

### 4.4 Web Worker for RFB Parsing

**Files:** New `web/src/rfb-worker.ts`, modifications to `web/src/connection.ts`

**What to build:**
- Move RFB message parsing and decompression (zlib, JPEG decode) to a dedicated Web Worker.
- Main thread only handles rendering and input events.
- Communication via `postMessage` with `Transferable` (ArrayBuffer transfer, not copy).
- Benefits: keeps the UI responsive during large framebuffer updates or heavy decompression.
- Fallback: if `Worker` is not available (e.g., some restricted environments), run inline.

---

### 4.5 Documentation

**Files:** New docs in `docs/`

1. **`docs/DEPLOYMENT.md`** — Production deployment guide:
   - Reverse proxy configuration (nginx, Caddy) with WebSocket passthrough.
   - TLS termination at the reverse proxy vs. at the redvnc-wsproxy.
   - Firewall rules (only expose proxy port, not VNC server ports).
   - Systemd unit file example.
   - Docker deployment with resource limits.

2. **`docs/SECURITY.md`** — Security hardening guide:
   - Recommended config for internet-facing deployments.
   - VNC auth limitations and why TLS is essential.
   - File upload security considerations.
   - Network segmentation recommendations (proxy in DMZ, VNC servers in private network).

3. **`docs/PERFORMANCE.md`** — Tuning guide:
   - Encoding selection guide (when to use Raw, Zlib, Tight, ZRLE).
   - Resolution and color depth tradeoffs.
   - Frame rate configuration.
   - Browser client renderer selection.

---

## Phase 5: UDP (WebRTC) Transport for H.264 Video

Opt-in, H.264-only unreliable transport that bypasses TCP head-of-line blocking. Delivers meaningful latency and stability gains on lossy links (WiFi, 4G/5G); near-zero benefit on clean LAN. See the research notes in the "Research: UDP vs TCP for RedVNC" section of `CLAUDE.md` for the underlying rationale and expected gains.

**Scope boundary — critical:** Only H.264 NALUs travel over the UDP DataChannel. Tight/Zlib/ZRLE MUST continue over the existing WebSocket path because they rely on persistent zlib stream dictionaries (invariants #1 and #6 in `CLAUDE.md`). A lost UDP packet would permanently desync those decoders.

**Expected gains (per research):**
- LAN: ~0% FPS, −2 to −5 ms latency, no quality change.
- Typical home WiFi (0.5–2% loss, 10–30 ms RTT): P95 jitter cut ~50%, −15 to −40 ms latency.
- Bad WiFi / 4G (3–10% loss, 50–100 ms RTT): 1.5–3× more stable FPS, −100 to −500 ms latency.
- Tight/Zlib/ZRLE: unchanged (still WebSocket).

---

### 5.1 WebRTC Signalling on the Proxy

**Files:** New `wsproxy/webrtc.go`, modifications to `wsproxy/server.go`, `wsproxy/config.go`, `wsproxy/proxy.go`

**What to build:**
- Add `pion/webrtc/v4` as a Go dependency (pure-Go WebRTC stack, no CGo).
- Extend `Config` with UDP-transport knobs: `WebRTCEnabled bool`, `WebRTCPortRange [2]uint16` (for ICE UDP allocation), `WebRTCPublicIP string` (for NAT'd deployments), `WebRTCSTUNServers []string` (default `stun:stun.l.google.com:19302`), `WebRTCTURNServers []TURNConfig`. Wire env vars in `wsproxy/config.go`.
- New extension messages (all initiated over the existing WebSocket, which stays the signalling and control channel):
  - `136` — `ExtWebRTCOffer` (proxy→browser): SDP offer emitted by the proxy when the browser signals `WebRTCCapable=true` in a client-hello extension.
  - `137` — `ExtWebRTCAnswer` (browser→proxy): SDP answer from the browser.
  - `138` — `ExtWebRTCICE` (bidirectional): trickle ICE candidates.
  - `139` — `ExtWebRTCReady` (proxy→browser): signals that the DataChannel is open and video will now flow over it.
  - `140` — `ExtWebRTCTeardown` (bidirectional): explicit close reason; triggers fallback to WebSocket transport for video.
- Create two DataChannels on the offer:
  - `video` — `ordered: false`, `maxRetransmits: 0` (unreliable/unordered). Carries H.264 NALUs only.
  - `control` — `ordered: true` (reliable). Carries keyframe requests and bandwidth hints. Small volume.
- On DataChannel open, mark the session as `videoTransport = "webrtc"`; on close/error, revert to `"websocket"` and log with `session_id`.
- Do NOT move Tight/Zlib/ZRLE/Raw traffic onto the DataChannel — those stay on the WebSocket unconditionally.

**Testing:**
- Unit test: SDP offer/answer round-trip against `pion/webrtc` in loopback mode.
- Integration test: full connect over loopback with `WebRTCEnabled=true`, verify DataChannel opens within 3 s.

---

### 5.2 WebRTC Signalling on the Browser

**Files:** New `web/src/webrtc-transport.ts`, modifications to `web/src/index.ts`, `web/src/connection.ts`, `web/src/types.ts`

**What to build:**
- Extend `VncClientOptions` with `preferUDP?: boolean` (default `false`) and `iceServers?: RTCIceServer[]`.
- On connect, if `preferUDP === true` AND the negotiated encoding is or will be H.264, include a `WebRTCCapable` bit in the initial client-hello extension.
- On receiving `ExtWebRTCOffer`, create `RTCPeerConnection`, call `setRemoteDescription`, generate answer, send `ExtWebRTCAnswer`.
- Trickle-ICE: each `onicecandidate` sends `ExtWebRTCICE`; each incoming `ExtWebRTCICE` calls `addIceCandidate`.
- Handle both DataChannels:
  - `video`: `onmessage` feeds NALUs into the existing H.264 decode pipeline (see 5.3).
  - `control`: reliable channel for keyframe requests initiated by the browser.
- 3-second ICE connection timeout. On failure, tear down the peer connection and continue on the WebSocket path unchanged (video keeps flowing over WS). Fire a `webrtc-fallback` event on `VncClient` so the UI can show a warning if desired.
- Watch `connectionState`: if it transitions to `disconnected` or `failed` mid-session, tear down and revert to WebSocket transport.

**Testing:**
- Vitest: mock `RTCPeerConnection`, verify SDP round-trip and ICE candidate exchange sequencing.
- Manual smoke test with the demo app against a local `redvnc-wsproxy` with WebRTC enabled.

---

### 5.3 H.264 NAL Packetization over DataChannel

**Files:** New `wsproxy/h264packet.go`, new `web/src/encodings/h264-udp.ts`, modifications to `wsproxy/proxy.go`, `h264/encoder.go`, `web/src/encodings/h264.ts`

**What to build:**

Server side:
- Intercept H.264 rectangles in the VNC→browser relay. When the active transport for the session is `webrtc`, extract the NALU payload (drop the RFB rectangle header — the browser reconstructs frame boundaries from NALU type) and send it via the `video` DataChannel; otherwise forward untouched over WebSocket.
- Fragment NALUs larger than the DataChannel MTU (SCTP-safe: 1200 bytes) using RFC 6184 FU-A style framing:
  - Byte 0: sequence number (uint16 wraparound) — 2 bytes.
  - Byte 2: flags — bit 0 = first-fragment, bit 1 = last-fragment, bit 2 = IDR, bits 3–7 = reserved.
  - Byte 3: NAL type (5 bits) + reserved (3 bits).
  - Bytes 4–7: presentation timestamp in ms since session start (uint32).
  - Bytes 8+: NALU fragment.
- Never reorder NALUs on the server side. Sequence numbers are assigned monotonically at fragmentation time.

Browser side:
- New `H264UdpAssembler` in `web/src/encodings/h264-udp.ts` reassembles fragments by sequence number, buffers up to `jitterBufferMs` (default 20 ms) worth of packets, then emits complete NALUs into the existing `H264Decoder`.
- On gap detection (missing sequence number after `jitterBufferMs`): drop the entire access unit up to the next IDR AND send `ExtWebRTCRequestIDR` over the control channel. Do NOT feed a partial access unit to `VideoDecoder` — it will produce visible corruption.
- Reuse the existing `H264Decoder`, `pendingFrames`, and `onFrameRendered` instrumentation from `web/src/encodings/h264.ts`. The only difference is the input source (`DataChannel.onmessage` vs framebuffer rect).

**Testing:**
- Go unit test in `wsproxy/h264packet_test.go`: NALU fragmentation round-trip.
- Vitest: assembler reorder tolerance (in-window shuffle), gap detection triggers IDR request, oversized-gap discards to next IDR.

---

### 5.4 Keyframe Request and Loss Recovery

**Files:** Modifications to `h264/encoder.go`, `h264/mf_windows.go`, `h264/x264_cgo.go`, `wsproxy/proxy.go`, new `wsproxy/h264control.go`

**What to build:**
- New extension message `141` — `ExtH264ForceIDR` (browser→proxy via the reliable `control` DataChannel; also accepted over WebSocket as fallback).
- On receipt, the proxy forwards a `ForceKeyframe()` call to the encoder for that session.
- `H264Encoder` interface gains `ForceKeyframe() error`:
  - Media Foundation: `IMFTransform::ProcessMessage(MFT_MESSAGE_COMMAND_REQUEST_KEY_FRAME, 0)` before the next input sample.
  - x264 (via CGo): set `x264_picture_t.i_type = X264_TYPE_IDR` on the next `x264_encoder_encode`.
- Rate-limit keyframe requests to at most 1 per 500 ms per session (prevent request storms from causing IDR spam that trashes bitrate).
- Log keyframe requests at `INFO` level with `session_id`, `reason` ("gap", "startup", "user"), and `intervalMs` since last IDR — this is a key metric for the perf-tuning docs.

**Testing:**
- Windows integration test: force IDR, verify the next output NALU is type 5 (IDR).
- x264 integration test: same, verify `x264_nal_t.i_type == NAL_SLICE_IDR`.

---

### 5.5 Adaptive Bitrate Feedback

**Files:** Modifications to `wsproxy/proxy.go`, `h264/mf_windows.go`, `h264/x264_cgo.go`, new `wsproxy/h264control.go`

**What to build:**
- WebRTC's built-in congestion controller (Google Congestion Control in `pion/webrtc`) estimates available bandwidth. Poll `RTCPeerConnection.GetStats()` server-side every 1 s and read `availableOutgoingBitrate`.
- Feed the estimate into the encoder as a target bitrate hint via new `H264Encoder.SetBitrate(bps uint32) error`:
  - Media Foundation: `ICodecAPI::SetValue(CODECAPI_AVEncCommonMeanBitRate, ...)`. Currently hard-coded to `5_000_000` bps at [h264/mf_windows.go:309](h264/mf_windows.go:309) — replace with a mutable value guarded by a mutex.
  - x264: `x264_encoder_reconfig` with the updated `i_bitrate`.
- Clamp: min 500 kbps, max = `MaxBitrate` config (default 20 Mbps). Rate-limit updates to at most 1 per 2 s to avoid encoder thrashing.
- When the session transport is WebSocket (fallback path), skip the estimator and use the static configured bitrate.
- Consider dropping Windows MFT from CBR → `UnconstrainedVBR` at the same time (already listed as an independent next-step in `CLAUDE.md`) — with adaptive bitrate feedback, CBR's fixed idle floor becomes an active liability.

**Testing:**
- Unit test the bitrate-clamp / rate-limit logic.
- Manual test with `tc netem` (see 5.7) that a simulated bandwidth drop is reflected in encoder output within 3 s.

---

### 5.6 Fallback, Negotiation, and Health Signalling

**Files:** Modifications to `wsproxy/proxy.go`, `web/src/index.ts`, `web/src/components/DebugOverlay.tsx`

**What to build:**
- Auto-negotiation flow:
  1. Browser opens WebSocket, sends client-hello with `webrtcCapable = preferUDP && supportsH264`.
  2. Proxy responds with SDP offer only if `WebRTCEnabled && capable`.
  3. If ICE completes within 3 s → set session `videoTransport = "webrtc"`, send `ExtWebRTCReady`, stop mirroring H.264 rects to WebSocket.
  4. If ICE fails or times out → tear down peer connection, log, continue on WebSocket unchanged. No user-visible interruption.
- Runtime transport swap:
  - If DataChannel `close` fires mid-session, revert to WebSocket H.264 relay within one FBU cycle. Force an IDR when the swap happens so the browser doesn't decode against a stale reference frame.
  - Emit an `ExtWebRTCTeardown` with `reason` field so the browser knows the swap happened.
- Debug overlay: add a "Transport" row displaying `websocket` or `webrtc (rtt Xms, loss Y%, bwe Z Mbps)`. Values sourced from `RTCPeerConnection.GetStats()` on the browser side.
- Metrics: add Prometheus counters `redvnc_video_transport_total{transport}` and `redvnc_webrtc_fallback_total{reason}` (ties into Phase 2.2).

**Testing:**
- Integration test simulating ICE timeout (block UDP with iptables in a container) — verify the connection continues over WebSocket without user-visible error.

---

### 5.7 Loss and Jitter Test Harness

**Files:** New `docs/PERF_TESTING.md`, new `scripts/netem-setup.sh` (Linux) / `scripts/clumsy-profile.xml` (Windows)

**What to build:**
- Automated `tc netem` profiles for Linux for reproducible perf runs. Standard profiles: `lan` (0% loss, 0 ms), `wifi-good` (0.5% loss, 10 ms, ±2 ms jitter), `wifi-bad` (3% loss, 40 ms, ±10 ms jitter), `mobile` (7% loss, 80 ms, ±30 ms jitter, 5 Mbps rate cap).
- Instrumentation harness: capture the per-frame overlay logs already emitted by [web/src/encodings/h264.ts](web/src/encodings/h264.ts) — `decode+readback avg/max`, `renderTime - fbuRequestTime`. Export as CSV for A/B comparison.
- Baseline the current WebSocket path under each profile, then re-run with `preferUDP=true`. Table the deltas. Publish the results in `docs/PERFORMANCE.md`.
- Do NOT ship UDP by default until this harness has produced numbers on at least the `wifi-good` and `wifi-bad` profiles showing measurable improvement.

**Testing:**
- The harness itself is the test. Also add a CI smoke job (Linux only) that runs one iteration under the `lan` profile to catch regressions.

---

### 5.8 Documentation

**Files:** New `docs/UDP_TRANSPORT.md`, updates to `docs/PERFORMANCE.md`, `README.md`, `CLAUDE.md`

**What to build:**
- `docs/UDP_TRANSPORT.md`:
  - When to enable `preferUDP` (H.264 users on lossy links) and when not to (LAN deployments, non-H.264 encodings).
  - Firewall / NAT requirements: symmetric NAT problems, when TURN is needed, port-range recommendations.
  - Extension message reference (136–141) with byte-level layouts.
  - Fallback behaviour and how to detect it in the browser API (`webrtc-fallback` event).
- `docs/PERFORMANCE.md`: add a section quoting the measured numbers from 5.7.
- `README.md`: a short note under "Configuration" pointing at `docs/UDP_TRANSPORT.md`.
- `CLAUDE.md`: update the "Data Flow" diagram to show the optional WebRTC DataChannel path, and add invariants (H.264-only, WS is source of truth for signalling, IDR-on-swap).

---

## Implementation Order (Suggested Timeline)

| Order | Item | Phase | Est. Complexity |
|-------|------|-------|-----------------|
| 1 | 1.5 Auth rate limiting | P1 | Small |
| 2 | 1.6 Secure proxy defaults | P1 | Small |
| 3 | 2.1 Structured logging | P2 | Medium |
| 4 | 2.3 Health check endpoints | P2 | Small |
| 5 | 2.4 Graceful shutdown | P2 | Small |
| 6 | 2.6 Expand CI test coverage | P2 | Small |
| 7 | 1.1 Windows screen capture | P1 | Large |
| 8 | 1.2 Windows input injection | P1 | Large |
| 9 | 1.3 Linux screen capture | P1 | Large |
| 10 | 1.4 Linux input injection | P1 | Medium |
| 11 | 2.5 Browser auto-reconnect | P2 | Medium |
| 12 | 2.7 Browser client unit tests | P2 | Medium |
| 13 | 2.8 Integration tests | P2 | Medium |
| 14 | 2.2 Prometheus metrics | P2 | Medium |
| 15 | 3.3 Configuration file support | P3 | Medium |
| 16 | 3.4 Dockerfile & Docker Compose | P3 | Small |
| 17 | 3.5 Versioning & changelog | P3 | Small |
| 18 | 3.1 ZRLE encoding | P3 | Large |
| 19 | 3.2 Adaptive encoding | P3 | Large |
| 20 | 4.1 macOS platform support | P4 | Large |
| 21 | 4.2 Multi-monitor support | P4 | Medium |
| 22 | 4.3 WebGL renderer | P4 | Medium |
| 23 | 4.4 Web Worker parsing | P4 | Medium |
| 24 | 4.5 Documentation | P4 | Medium |
| 25 | 5.7 Loss/jitter test harness (baseline WS numbers) | P5 | Small |
| 26 | 5.1 WebRTC signalling on proxy | P5 | Large |
| 27 | 5.2 WebRTC signalling on browser | P5 | Medium |
| 28 | 5.3 H.264 NAL packetization | P5 | Large |
| 29 | 5.4 Keyframe request & loss recovery | P5 | Medium |
| 30 | 5.6 Fallback & negotiation | P5 | Medium |
| 31 | 5.5 Adaptive bitrate feedback | P5 | Medium |
| 32 | 5.8 Documentation | P5 | Small |
