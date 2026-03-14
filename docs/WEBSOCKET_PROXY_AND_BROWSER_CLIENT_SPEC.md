# WebSocket VNC Proxy & Browser Client Specification

## Overview

This document specifies two new components for the redvnc project:

1. **`wsproxy`** — A Go WebSocket-to-TCP proxy that bridges browser clients to VNC servers. The proxy handles multiple simultaneous client connections, each to a potentially different VNC server. It supports extended message types for clipboard sync and file upload.
2. **`web`** — A React/TypeScript browser client library that renders VNC framebuffers on a `<canvas>`, handles user input, clipboard integration, and file uploads. The client specifies which VNC server (IP:port) to connect to when opening the WebSocket.

Each client–proxy session communicates over a single WebSocket connection using a binary protocol that wraps standard RFB messages and adds custom extension messages. The proxy manages all sessions independently and concurrently.

---

## Architecture

```
┌──────────────────┐                                    ┌──────────────────┐                          ┌──────────────────┐
│  Browser Client  │ ──  WebSocket (wss://)  ──────────►│                  │ ──  TCP (10.0.0.1:5900)─►│  VNC Server A    │
│  (React/TS)      │   target=10.0.0.1:5900             │                  │                          └──────────────────┘
└──────────────────┘                                    │                  │
                                                        │   wsproxy (Go)   │
┌──────────────────┐                                    │                  │                          ┌──────────────────┐
│  Browser Client  │ ──  WebSocket (wss://)  ──────────►│                  │ ──  TCP (10.0.0.2:5901)─►│  VNC Server B    │
│  (React/TS)      │   target=10.0.0.2:5901             │                  │                          └──────────────────┘
└──────────────────┘                                    │                  │
                                                        │                  │                          ┌──────────────────┐
┌──────────────────┐                                    │                  │ ──  TCP (10.0.0.1:5900)─►│  VNC Server A    │
│  Browser Client  │ ──  WebSocket (wss://)  ──────────►│                  │     (2nd connection)     │  (shared)        │
│  (React/TS)      │   target=10.0.0.1:5900             │                  │                          └──────────────────┘
└──────────────────┘                                    └──────────────────┘
```

The proxy is **not** a VNC client itself — it performs a byte-level relay of the RFB protocol between each WebSocket and its corresponding TCP connection. Extension messages (clipboard, file upload) are intercepted and handled by the proxy before/after relay. Each client connection results in an independent TCP connection to the target VNC server; the proxy does not multiplex or share TCP connections between clients.

---

## 1. WebSocket Proxy (`wsproxy`)

### 1.1 Package Location

```
wsproxy/
├── proxy.go          # Core proxy logic, WebSocket ↔ TCP relay (per-session)
├── session.go        # Session lifecycle and state management
├── server.go         # HTTP server, WebSocket upgrade, configuration, connection manager
├── clipboard.go      # Clipboard extension message handling
├── fileupload.go     # File upload extension message handling
└── wsproxy_test.go   # Tests
```

### 1.2 Configuration

```go
type Config struct {
    // ListenAddr is the HTTP/WebSocket listen address (e.g. ":8080").
    ListenAddr string

    // AllowedVNCTargets is a whitelist of VNC server addresses (host:port) that
    // clients are permitted to connect to. Each entry is a "host:port" string.
    // If empty, clients may connect to ANY target (open relay — use with caution).
    // The proxy rejects connection requests to targets not in this list.
    AllowedVNCTargets []string

    // DefaultVNCPassword is the VNC authentication password used when the
    // client does not supply one. Empty means no auth.
    DefaultVNCPassword string

    // MaxConnections is the maximum number of simultaneous client sessions
    // (WebSocket ↔ TCP pairs) the proxy will accept. New connections beyond
    // this limit receive HTTP 503 Service Unavailable. Default: 100.
    MaxConnections int

    // MaxConnectionsPerTarget limits how many simultaneous sessions can
    // connect to the same VNC target (host:port). 0 means unlimited.
    // Default: 10.
    MaxConnectionsPerTarget int

    // DefaultUploadDir is the fallback directory used when the client does not
    // specify a destination in UploadBegin. Defaults to the OS-specific
    // Downloads folder (see §4).
    DefaultUploadDir string

    // AllowedUploadDirs is a whitelist of directories the client is permitted
    // to upload into. If empty, only DefaultUploadDir is allowed. Each entry
    // is resolved to an absolute path at startup. The proxy rejects any
    // client-requested directory that is not a subdirectory of (or equal to)
    // one of these entries.
    AllowedUploadDirs []string

    // MaxUploadSize is the maximum file upload size in bytes. Default: 100MB.
    MaxUploadSize int64

    // TLSCertFile and TLSKeyFile enable TLS for the WebSocket server.
    TLSCertFile string
    TLSKeyFile  string

    // AllowedOrigins restricts WebSocket connections by Origin header.
    // Empty means allow all origins.
    AllowedOrigins []string
}
```

### 1.3 Connection Lifecycle

1. Browser opens WebSocket to `ws(s)://host:port/ws?target=<ip:port>&password=<optional>`
   - The **`target`** query parameter is **required** and specifies the VNC server address the client wants to connect to (e.g. `target=192.168.1.50:5900`).
   - The `password` query parameter is optional and overrides `DefaultVNCPassword` for this session.
2. Proxy validates the request:
   - Checks that `target` is present and well-formed (`host:port`)
   - If `AllowedVNCTargets` is non-empty, verifies the target is in the allowlist. If not, the proxy rejects the WebSocket upgrade with HTTP 403 Forbidden and a JSON error body: `{"error": "target not allowed"}`.
   - Checks that `MaxConnections` and `MaxConnectionsPerTarget` limits are not exceeded. If exceeded, responds with HTTP 503 Service Unavailable.
3. Proxy opens a new TCP connection to the VNC server at the client-specified `target`
4. Proxy performs the RFB handshake with the VNC server on behalf of the browser client:
   - Sends RFB 3.8 version
   - Handles security negotiation (None or VNCAuth using the client-supplied or default password)
   - Sends ClientInit with shared=1
   - Receives ServerInit
5. Proxy sends a **session init** extension message to the browser (see §3.1) containing the ServerInit data
6. From this point, the proxy enters **relay mode** for this session:
   - **Browser → VNC**: Binary WebSocket frames are parsed for the message type byte. Standard RFB client messages (types 0–6) are forwarded to the VNC TCP connection as-is. Extension messages (types 128+) are intercepted and handled by the proxy.
   - **VNC → Browser**: TCP data from the VNC server is forwarded to the browser as binary WebSocket frames, unchanged. The proxy also injects extension messages when needed (e.g., clipboard pushes).

Each WebSocket connection creates an independent session with its own TCP connection to the target VNC server. Multiple clients can connect to the same or different VNC servers simultaneously.

### 1.4 VNC Handshake Delegation

The proxy performs the full RFB handshake with the VNC server so the browser never needs to implement RFB handshake logic. This means:

- The browser receives framebuffer dimensions, pixel format, and server name via the session init extension message
- The browser immediately starts sending `SetPixelFormat`, `SetEncodings`, and `FramebufferUpdateRequest` messages (standard RFB, relayed directly)
- The browser receives `FramebufferUpdate`, `ServerCutText`, and `Bell` messages from the VNC server (relayed directly)

### 1.5 Session Teardown

On WebSocket close or TCP disconnect, the proxy tears down both sides of that individual session and cleans up any in-progress file upload state. Other active sessions are not affected.

### 1.6 Connection Management

The proxy maintains a connection manager that tracks all active sessions. Each session is identified by a unique session ID (UUID) assigned at WebSocket upgrade time.

**Session state tracked per connection:**
- Session ID (UUID)
- Client remote address
- Target VNC address (from the `target` query parameter)
- WebSocket connection handle
- TCP connection handle
- Active upload state (upload IDs, partial files)
- Connection timestamps (created, last activity)

**Concurrency controls:**
- `MaxConnections` limits the total number of active sessions across all targets. Default: 100.
- `MaxConnectionsPerTarget` limits how many sessions can be connected to the same `host:port` at once. Default: 10. This prevents a single target from being overwhelmed.
- Connection counts are updated atomically on session creation and teardown.

**Graceful shutdown:**
- On SIGTERM/SIGINT, the proxy stops accepting new WebSocket connections, sends a WebSocket close frame to all active clients, waits up to 10 seconds for sessions to drain, then forcefully closes remaining connections.

### 1.7 Proxy Shutdown

On process shutdown, the proxy gracefully drains all active sessions as described in §1.6.

---

## 2. Browser Client (`web`)

### 2.1 Package Location

```
web/
├── package.json
├── tsconfig.json
├── vite.config.ts            # Build config (library mode)
├── src/
│   ├── index.ts              # Public API exports
│   ├── connection.ts         # WebSocket connection manager
│   ├── rfb-parser.ts         # Binary RFB message parser (server→client)
│   ├── rfb-writer.ts         # Binary RFB message builder (client→server)
│   ├── framebuffer.ts        # Framebuffer state, pixel decoding, dirty tracking
│   ├── renderer.ts           # Canvas 2D rendering from framebuffer
│   ├── input.ts              # Keyboard/mouse → RFB event translation
│   ├── clipboard.ts          # Browser Clipboard API ↔ extension messages
│   ├── file-upload.ts        # File upload via extension messages
│   ├── encodings/
│   │   ├── raw.ts            # Raw encoding decoder
│   │   ├── copyrect.ts       # CopyRect decoder
│   │   ├── zlib.ts           # Zlib encoding decoder (using DecompressionStream)
│   │   └── tight.ts          # Tight encoding decoder (zlib + JPEG)
│   ├── components/
│   │   ├── VncViewer.tsx      # Main React component
│   │   ├── FileUpload.tsx     # Drag-and-drop / file picker UI
│   │   └── Toolbar.tsx        # Optional toolbar (clipboard, upload, fullscreen)
│   └── types.ts               # Shared TypeScript type definitions
└── demo/
    ├── index.html
    └── main.tsx               # Minimal demo app
```

### 2.2 Public API

```typescript
// Core connection class (framework-agnostic)
export class VncClient {
  constructor(options: VncClientOptions);

  connect(): Promise<void>;
  disconnect(): void;

  // State
  readonly connected: boolean;
  readonly width: number;
  readonly height: number;
  readonly name: string;

  // Attach to a canvas element for rendering
  attachCanvas(canvas: HTMLCanvasElement): void;
  detachCanvas(): void;

  // Clipboard
  sendClipboard(text: string): void;
  onClipboard(callback: (text: string) => void): void;

  // File upload
  uploadFile(file: File, options?: UploadOptions): Promise<UploadResult>;
  onUploadProgress(callback: (progress: UploadProgress) => void): void;

  // Configure the remote upload directory (sent in UploadBegin)
  setUploadDir(path: string): void;

  // Events
  on(event: 'connect', cb: () => void): void;
  on(event: 'disconnect', cb: (reason: string) => void): void;
  on(event: 'resize', cb: (width: number, height: number) => void): void;
  on(event: 'bell', cb: () => void): void;
  on(event: 'clipboard', cb: (text: string) => void): void;
}

interface VncClientOptions {
  url: string;           // WebSocket URL: "ws://host:port/ws"
  target: string;        // VNC server address to connect to (e.g. "192.168.1.50:5900")
  password?: string;     // VNC password, sent as query param
  viewOnly?: boolean;    // Disable input events
  scaleToFit?: boolean;  // Scale framebuffer to canvas size
  clipboardSync?: boolean; // Auto-sync browser clipboard (default: true)
  encodings?: number[];  // Preferred encodings (default: [7, 6, 1, 0])
  uploadDir?: string;    // Remote directory for file uploads (default: server's DefaultUploadDir)
}

// React component
export const VncViewer: React.FC<VncViewerProps>;

interface UploadOptions {
  dir?: string;          // Override upload directory for this file
}

interface VncViewerProps {
  url: string;
  target: string;        // VNC server address (e.g. "192.168.1.50:5900")
  password?: string;
  viewOnly?: boolean;
  scaleToFit?: boolean;
  clipboardSync?: boolean;
  uploadDir?: string;
  onConnect?: () => void;
  onDisconnect?: (reason: string) => void;
  onBell?: () => void;
  className?: string;
  style?: React.CSSProperties;
}
```

### 2.3 Framebuffer Rendering

The client maintains an off-screen `ImageData` buffer matching the VNC server's framebuffer dimensions. When `FramebufferUpdate` rectangles arrive:

1. Each rectangle is decoded based on its encoding type (Raw, CopyRect, Zlib, Tight)
2. Decoded RGBA pixels are written into the `ImageData` buffer at the rectangle's (x, y) position
3. Dirty regions are tracked and only the changed area is painted to the canvas via `putImageData()`
4. For Tight JPEG tiles, the JPEG blob is decoded via `createImageBitmap()` and drawn with `drawBitmap()` into the buffer

**Pixel format negotiation**: After receiving the session init, the browser sends `SetPixelFormat` requesting 32-bit RGBA (redShift=0, greenShift=8, blueShift=16) which maps directly to `ImageData`'s format, avoiding per-pixel conversion.

**Requested encodings**: The browser sends `SetEncodings` with `[Tight(7), Zlib(6), CopyRect(1), Raw(0), Cursor(-239), DesktopSize(-223)]`.

### 2.4 Input Handling

**Keyboard**: The `input.ts` module listens for `keydown`/`keyup` on the canvas element and converts `KeyboardEvent.code` and `KeyboardEvent.key` to X11 keysym values. A lookup table maps standard keys; Unicode codepoints are used for characters above U+00FF (keysym = 0x01000000 + codepoint).

**Mouse/Pointer**: `mousemove`, `mousedown`, `mouseup`, and `wheel` events on the canvas are translated to RFB PointerEvent messages. Coordinates are adjusted for canvas scaling. Scroll wheel maps to buttons 4/5 (up/down) per RFB convention.

**Touch**: Basic touch support converts single-touch events to pointer events. Multi-touch pinch is mapped to scroll for zoom-like behavior.

### 2.5 Cursor Handling

When the server sends a Cursor pseudo-encoding rectangle:
1. The pixel data and bitmask are decoded into an RGBA image
2. A CSS `cursor: url(data:image/png;...) hotX hotY, auto` is applied to the canvas
3. The canvas hides the system cursor by setting a custom cursor image

---

## 3. Extension Protocol

Standard RFB message types use types 0–127. Extension messages use types **128–255** and are only exchanged between the browser and the proxy (never forwarded to the VNC server).

All extension messages are binary, big-endian, and follow this envelope:

```
┌─────────┬──────────┬──────────────────┐
│ type(1) │ len(4)   │ payload(len)     │
│ uint8   │ uint32BE │ bytes            │
└─────────┴──────────┴──────────────────┘
```

### 3.1 Session Init (Server → Browser)

Sent by the proxy after completing the RFB handshake with the VNC server.

| Field | Type | Description |
|-------|------|-------------|
| type | uint8 | 128 |
| length | uint32 | Payload length |
| width | uint16 | Framebuffer width |
| height | uint16 | Framebuffer height |
| pixelFormat | 16 bytes | RFB PixelFormat struct |
| nameLength | uint32 | Desktop name length |
| name | bytes | UTF-8 desktop name |

### 3.2 Clipboard Sync (Bidirectional)

Extends the standard RFB `ClientCutText`/`ServerCutText` with richer clipboard support.

**Browser → Proxy** (type 129): Set server clipboard

| Field | Type | Description |
|-------|------|-------------|
| type | uint8 | 129 |
| length | uint32 | Payload length |
| textLength | uint32 | Text byte length |
| text | bytes | UTF-8 clipboard text |

On receiving this, the proxy:
1. Sends a standard RFB `ClientCutText` (type 6) to the VNC server
2. Optionally interacts with the host OS clipboard (xclip/xsel on Linux, pbcopy on macOS, clip.exe on Windows) to set the system clipboard

**Proxy → Browser** (type 130): Server clipboard update

| Field | Type | Description |
|-------|------|-------------|
| type | uint8 | 130 |
| length | uint32 | Payload length |
| textLength | uint32 | Text byte length |
| text | bytes | UTF-8 clipboard text |

Triggered when the proxy intercepts a standard RFB `ServerCutText` (type 3) from the VNC server. The proxy forwards the original RFB message to the browser AND sends this extension message. The browser uses the Clipboard API (`navigator.clipboard.writeText()`) to set the local clipboard.

### 3.3 File Upload Protocol

File uploads use a chunked transfer protocol to support large files and progress reporting.

#### 3.3.1 Upload Begin (Browser → Proxy)

| Field | Type | Description |
|-------|------|-------------|
| type | uint8 | 131 |
| length | uint32 | Payload length |
| uploadId | uint32 | Client-assigned upload ID |
| fileSize | uint64 | Total file size in bytes |
| dirLength | uint16 | Upload directory path byte length (0 = use server default) |
| dir | bytes | UTF-8 absolute directory path on the remote machine |
| nameLength | uint16 | Filename byte length |
| fileName | bytes | UTF-8 filename (basename only, no path separators) |

The **client** specifies the destination directory. If `dirLength` is 0, the proxy uses its `DefaultUploadDir`.

The proxy validates:
- **Directory authorization**: The resolved absolute path of `dir` must be equal to or a subdirectory of one of the `AllowedUploadDirs` entries (or `DefaultUploadDir` if `AllowedUploadDirs` is empty). If the check fails, the proxy responds with UploadStatus status=1 and error "directory not allowed".
- **Directory existence**: The proxy creates the directory (and parents) if it doesn't exist, provided it passes the authorization check.
- Filename does not contain path separators or `..`
- File size does not exceed `MaxUploadSize`
- Sanitizes the filename (removes special characters, preserves extension)
- Creates the destination file at `{dir}/{sanitized_filename}`
- If a file with the same name exists, appends a numeric suffix: `file(1).txt`, `file(2).txt`, etc.

#### 3.3.2 Upload Chunk (Browser → Proxy)

| Field | Type | Description |
|-------|------|-------------|
| type | uint8 | 132 |
| length | uint32 | Payload length |
| uploadId | uint32 | Upload ID from UploadBegin |
| offset | uint64 | Byte offset in the file |
| chunkData | bytes | File chunk (max 64KB per chunk) |

The proxy writes the chunk at the specified offset. Chunks may arrive out of order (though the browser will send them sequentially).

#### 3.3.3 Upload End (Browser → Proxy)

| Field | Type | Description |
|-------|------|-------------|
| type | uint8 | 133 |
| length | uint32 | Payload length |
| uploadId | uint32 | Upload ID |
| checksum | uint32 | CRC-32 of the entire file |

The proxy verifies the CRC-32 checksum, closes the file, and sends a status response.

#### 3.3.4 Upload Status (Proxy → Browser)

| Field | Type | Description |
|-------|------|-------------|
| type | uint8 | 134 |
| length | uint32 | Payload length |
| uploadId | uint32 | Upload ID |
| status | uint8 | 0=success, 1=error |
| bytesWritten | uint64 | Bytes written so far |
| messageLength | uint16 | Status message length |
| message | bytes | UTF-8 status/error message |

Sent after each chunk (for progress) and after UploadEnd (for final status). The `bytesWritten` field lets the browser compute progress percentage.

#### 3.3.5 Upload Cancel (Browser → Proxy)

| Field | Type | Description |
|-------|------|-------------|
| type | uint8 | 135 |
| length | uint32 | Payload length |
| uploadId | uint32 | Upload ID |

Cancels an in-progress upload. The proxy deletes the partial file and sends an UploadStatus with status=1 and message "cancelled".

### 3.4 Extension Message Type Summary

| Type | Direction | Name |
|------|-----------|------|
| 128 | Proxy → Browser | SessionInit |
| 129 | Browser → Proxy | ClipboardSet |
| 130 | Proxy → Browser | ClipboardUpdate |
| 131 | Browser → Proxy | UploadBegin |
| 132 | Browser → Proxy | UploadChunk |
| 133 | Browser → Proxy | UploadEnd |
| 134 | Proxy → Browser | UploadStatus |
| 135 | Browser → Proxy | UploadCancel |

---

## 4. Upload Directory

The upload destination is **specified by the browser client** in each UploadBegin message. This allows different clients or upload operations to target different directories on the remote machine.

### 4.1 Client-Side Configuration

The client sets the upload directory via:
- `VncClientOptions.uploadDir` at construction time (applies to all uploads)
- `client.setUploadDir(path)` to change it at runtime
- `UploadOptions.dir` per individual `uploadFile()` call to override

If the client does not specify a directory (empty string or omitted), the proxy falls back to `DefaultUploadDir`.

### 4.2 Proxy-Side Defaults and Authorization

The proxy's `DefaultUploadDir` defaults to the OS-specific Downloads folder:

| OS | Default Path |
|----|-------------|
| Linux | `$XDG_DOWNLOAD_DIR` or `$HOME/Downloads` |
| macOS | `$HOME/Downloads` |
| Windows | `%USERPROFILE%\Downloads` |

The proxy's `AllowedUploadDirs` restricts which directories clients may write to. If the list is empty, only `DefaultUploadDir` is permitted. The proxy operator configures this via:
- CLI flag: `--default-upload-dir /path/to/dir`
- CLI flag: `--allowed-upload-dir /path/a --allowed-upload-dir /path/b` (repeatable)
- Config struct: `Config.DefaultUploadDir`, `Config.AllowedUploadDirs`

Note: Upload directories are local to the proxy host, not the VNC server. This is relevant when the proxy and VNC server run on different machines.

The directory is created if it doesn't exist (provided it passes the authorization check).

---

## 5. Security Considerations

1. **VNC target allowlisting**: When `AllowedVNCTargets` is configured, the proxy only permits connections to listed `host:port` pairs. Requests to unlisted targets are rejected at WebSocket upgrade time (HTTP 403). **If `AllowedVNCTargets` is empty, the proxy operates as an open relay — this should only be used in trusted networks.**
2. **Connection limits**: `MaxConnections` and `MaxConnectionsPerTarget` prevent resource exhaustion and protect individual VNC servers from being overwhelmed by too many proxied sessions.
3. **Origin checking**: The proxy validates the `Origin` header against `AllowedOrigins` to prevent unauthorized cross-origin connections.
4. **Filename sanitization**: Uploaded filenames are stripped of path separators, `..`, null bytes, and control characters. Only the basename is used.
5. **Upload size limits**: Enforced at both UploadBegin (reject immediately) and UploadChunk (track running total).
6. **Upload directory authorization**: The proxy resolves the client-requested directory to an absolute path, follows symlinks, and verifies it falls within `AllowedUploadDirs` (or `DefaultUploadDir`). Path traversal via `..` or symlinks outside allowed directories is rejected.
7. **WebSocket binary mode**: All frames use binary mode (opcode 0x02). Text frames are rejected.
8. **Password handling**: VNC passwords are sent to the proxy via query parameter over WSS (TLS). They are never logged. The `target` query parameter is also not logged in production mode.
9. **Rate limiting**: The proxy limits concurrent uploads per connection (max 4) and rejects new upload requests beyond the limit.
10. **Session isolation**: Each client session is fully isolated — a misbehaving or disconnected client cannot affect other sessions.

---

## 6. Wire Examples

### 6.1 Typical Session Flow

```
Browser                         Proxy                          VNC Server
   │                              │                          (10.0.0.5:5900)
   │                              │                               │
   │── WS Connect ───────────────►│                               │
   │   ?target=10.0.0.5:5900      │                               │
   │   &password=secret           │  (validate target allowlist)  │
   │                              │  (check connection limits)    │
   │                              │──── TCP Connect ──────────────►│
   │                              │◄─── RFB 003.008\n ────────────│
   │                              │──── RFB 003.008\n ────────────►│
   │                              │◄─── Security types ───────────│
   │                              │──── Security choice ──────────►│
   │                              │◄─── SecurityResult(0) ────────│
   │                              │──── ClientInit(shared=1) ─────►│
   │                              │◄─── ServerInit ───────────────│
   │◄─── SessionInit(128) ───────│                               │
   │                              │                               │
   │──── SetPixelFormat(0) ──────►│──── relay ───────────────────►│
   │──── SetEncodings(2) ────────►│──── relay ───────────────────►│
   │──── FBUpdateReq(3) ─────────►│──── relay ───────────────────►│
   │                              │◄─── FBUpdate(0) ─────────────│
   │◄─── relay ──────────────────│                               │
   │                              │                               │
   │──── KeyEvent(4) ────────────►│──── relay ───────────────────►│
   │──── PointerEvent(5) ────────►│──── relay ───────────────────►│
   │                              │                               │
   │──── ClipboardSet(129) ──────►│                               │
   │                              │──── ClientCutText(6) ─────────►│
   │                              │                               │
   │                              │◄─── ServerCutText(3) ────────│
   │◄─── relay (type 3) ─────────│                               │
   │◄─── ClipboardUpdate(130) ───│                               │
   │                              │                               │
   │──── UploadBegin(131) ───────►│  (creates file)              │
   │◄─── UploadStatus(134) ──────│                               │
   │──── UploadChunk(132) ───────►│  (writes chunk)              │
   │◄─── UploadStatus(134) ──────│                               │
   │──── UploadChunk(132) ───────►│  (writes chunk)              │
   │◄─── UploadStatus(134) ──────│                               │
   │──── UploadEnd(133) ─────────►│  (verifies CRC-32)           │
   │◄─── UploadStatus(134, ok) ──│                               │
   │                              │                               │
   │──── WS Close ───────────────►│──── TCP Close ───────────────►│
```

### 6.2 SetPixelFormat Sent by Browser

The browser requests RGBA pixel format for direct `ImageData` compatibility:

```
Byte offset   Value    Field
0             0x00     MsgSetPixelFormat
1-3           0x000000 padding
4             32       bitsPerPixel
5             24       depth
6             0        bigEndian (little)
7             1        trueColour
8-9           0x00FF   redMax (255)
10-11         0x00FF   greenMax (255)
12-13         0x00FF   blueMax (255)
14            0        redShift
15            8        greenShift
16            16       blueShift
17-19         0x000000 padding
```

### 6.3 Extension Message Example: UploadBegin

With a client-specified upload directory `/home/user/Documents`:

```
Byte offset   Value                       Field
0             0x83 (131)                   type
1-4           0x0000002E                   length (46 bytes)
5-8           0x00000001                   uploadId (1)
9-16          0x0000000000100000           fileSize (1MB)
17-18         0x0014                       dirLength (20)
19-38         "/home/user/Documents"       dir
39-40         0x0008                       nameLength (8)
41-48         "test.txt"                   fileName
```

With no directory (use server default):

```
Byte offset   Value                       Field
0             0x83 (131)                   type
1-4           0x00000016                   length (22 bytes)
5-8           0x00000001                   uploadId (1)
9-16          0x0000000000100000           fileSize (1MB)
17-18         0x0000                       dirLength (0 = use default)
19-20         0x0008                       nameLength (8)
21-28         "test.txt"                   fileName
```

---

## 7. Build & Run

### 7.1 Proxy

```bash
# Build
cd wsproxy && go build -o redvnc-wsproxy ./cmd

# Run as open relay (any VNC target allowed — use only in trusted networks)
./redvnc-wsproxy --listen :8080

# Restrict to specific VNC targets
./redvnc-wsproxy --listen :8080 \
  --allowed-vnc-target 10.0.0.5:5900 \
  --allowed-vnc-target 10.0.0.6:5900 \
  --allowed-vnc-target 192.168.1.100:5901

# With TLS
./redvnc-wsproxy --listen :8443 --tls-cert cert.pem --tls-key key.pem \
  --allowed-vnc-target 10.0.0.5:5900

# With connection limits
./redvnc-wsproxy --listen :8080 \
  --max-connections 200 \
  --max-connections-per-target 20 \
  --allowed-vnc-target 10.0.0.5:5900

# Custom default upload directory and allowed directories
./redvnc-wsproxy --listen :8080 \
  --allowed-vnc-target 10.0.0.5:5900 \
  --default-upload-dir /tmp/uploads \
  --allowed-upload-dir /tmp/uploads \
  --allowed-upload-dir /home/user/Documents
```

### 7.2 Browser Client

```bash
# Development
cd web && npm install && npm run dev

# Build library
npm run build

# Use as npm package
npm install @redvnc/web
```

```tsx
import { VncViewer } from '@redvnc/web';

function App() {
  return (
    <VncViewer
      url="ws://localhost:8080/ws"
      target="10.0.0.5:5900"
      scaleToFit
      onConnect={() => console.log('Connected')}
    />
  );
}
```

---

## 8. Dependencies

### Proxy (Go)

- `golang.org/x/net/websocket` or `nhooyr.io/websocket` — WebSocket server implementation
- No other external dependencies (reuses redvnc's `rfb` package for protocol types)

### Browser Client (TypeScript/React)

- **React** >= 18 (peer dependency)
- **pako** — zlib decompression (for Zlib encoding; alternative: native `DecompressionStream` API where available)
- No other runtime dependencies

---

## 9. Implementation Order

Recommended implementation sequence:

1. **Proxy: basic relay with target routing** — WebSocket upgrade, `target` query param parsing, target allowlist validation, TCP connect to client-specified target, RFB handshake, bidirectional byte relay, SessionInit message
2. **Proxy: connection manager** — Session tracking, `MaxConnections` / `MaxConnectionsPerTarget` enforcement, graceful shutdown with session draining
3. **Browser: connection + raw rendering** — WebSocket connect with `target` param, SessionInit parse, SetPixelFormat/SetEncodings, FramebufferUpdateRequest loop, Raw encoding decoder, canvas rendering
3. **Browser: input** — Keyboard keysym mapping, pointer events, touch events
4. **Browser: CopyRect + Zlib decoders** — Add remaining encoding decoders
5. **Browser: Tight decoder** — Tight encoding with JPEG tiles
6. **Proxy + Browser: clipboard** — Extension messages 129/130, OS clipboard integration in proxy
7. **Proxy + Browser: file upload** — Extension messages 131–135, chunked upload with progress
8. **Browser: cursor + desktop resize** — Pseudo-encoding handlers
9. **React component** — `VncViewer` wrapper, `Toolbar`, `FileUpload` components
10. **Polish** — Error handling, reconnection, TLS, origin checking, tests
