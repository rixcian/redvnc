package rfb

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// MultiEncoder encodes framebuffer pixel data into multiple RFB rectangles (e.g. tiles).
type MultiEncoder interface {
	EncodeMulti(x, y, width, height uint16, pixels []byte, stride int) ([]Rectangle, error)
	Type() int32
	Reset()
}

// TightEncoderFactory creates a new Tight encoder instance.
// Set this in ServerConfig to enable Tight encoding support.
type TightEncoderFactory func() MultiEncoder

// ScreenCapturer provides screen framebuffer data to the VNC server.
type ScreenCapturer interface {
	// Bounds returns the screen width and height.
	Bounds() (width, height uint16)
	// Capture captures the current screen into a pixel buffer.
	// Returns raw pixel data in the server's pixel format and the stride (bytes per row).
	Capture() (pixels []byte, stride int, err error)
}

// dirtyRectCapturer is an optional extension of ScreenCapturer. When the
// configured Capturer also implements this interface, the server queries
// dirty rectangles after each Capture() call and encodes only changed regions.
//
// Return conventions for LastDirtyRects:
//
//	nil  → full-frame change; encode everything.
//	[]   → no change since last frame; send empty FBU.
//	[..] → only these regions changed; encode selectively (Tight only).
type dirtyRectCapturer interface {
	LastDirtyRects() []image.Rectangle
}

// InputHandler processes keyboard and pointer events from VNC clients.
type InputHandler interface {
	// KeyEvent handles a key press or release.
	KeyEvent(down bool, key uint32)
	// PointerEvent handles a pointer movement or button click.
	PointerEvent(buttonMask uint8, x, y uint16)
}

// ClipboardSetter is an optional extension of InputHandler. When the server's
// InputHandler also implements ClipboardSetter, the server will call
// SetClipboard whenever a VNC client sends a ClientCutText message so the OS
// clipboard is updated and the focused application can paste it with Ctrl+V.
type ClipboardSetter interface {
	SetClipboard(text string) error
}

// CursorProvider supplies cursor image data to the server.
// The server calls Cursor() when building framebuffer updates for clients
// that support the Cursor pseudo-encoding.
type CursorProvider interface {
	// Cursor returns the current cursor image, or nil if no cursor update is needed.
	Cursor() *CursorImage
}

// SecurityHandler performs the security handshake with a client.
// Handshake may return a TLS-upgraded net.Conn (e.g. VeNCrypt). Non-upgrading
// handlers return conn unchanged. The caller must replace its conn/br/bw if
// the returned conn differs from the input conn.
type SecurityHandler interface {
	Type() uint8
	Handshake(conn net.Conn, br *bufio.Reader, bw *bufio.Writer) (net.Conn, error)
}

// ServerConfig holds configuration for the VNC server.
type ServerConfig struct {
	// Width and Height of the framebuffer. If a ScreenCapturer is set, these are ignored.
	Width  uint16
	Height uint16

	// Name is the desktop name advertised to clients.
	Name string

	// Security is the list of security handlers offered to clients.
	// If empty, SecurityNone is used.
	Security []SecurityHandler

	// Capturer provides screen data. If nil, a blank screen is served.
	Capturer ScreenCapturer

	// Input handles keyboard and pointer events. If nil, events are discarded.
	Input InputHandler

	// PixelFormat is the server's native pixel format. If zero, DefaultPixelFormat() is used.
	PixelFormat PixelFormat

	// NewTightEncoder creates a Tight encoder for a client connection.
	// If nil, Tight encoding is not available.
	NewTightEncoder TightEncoderFactory

	// CursorProvider supplies cursor images for the Cursor pseudo-encoding.
	// If nil, cursor pseudo-encoding is not used.
	CursorProvider CursorProvider

	// MaxFPS limits the maximum framebuffer update rate per client.
	// If zero, defaults to 30.
	MaxFPS int

	// TLSConfig enables TLS encryption on the server. If non-nil, the server
	// wraps each accepted connection in TLS before the RFB handshake.
	TLSConfig *tls.Config

	// Logger is the structured logger. If nil, slog.Default() is used.
	Logger *slog.Logger
}

// Server is an RFB protocol server that accepts VNC client connections.
type Server struct {
	config   ServerConfig
	listener net.Listener
	logger   *slog.Logger

	mu      sync.Mutex
	clients map[*ClientConn]struct{}
	done    chan struct{}
}

// NewServer creates a new VNC server with the given configuration.
func NewServer(config ServerConfig) *Server {
	if config.Name == "" {
		config.Name = "redvnc"
	}
	if config.PixelFormat.BitsPerPixel == 0 {
		config.PixelFormat = DefaultPixelFormat()
	}
	if len(config.Security) == 0 {
		config.Security = []SecurityHandler{&noneSecurity{}}
	}
	if config.MaxFPS <= 0 {
		config.MaxFPS = 30
	}
	if config.Capturer != nil {
		config.Width, config.Height = config.Capturer.Bounds()
		// Wrap the capturer in a PipelinedCapturer so capture overlaps with
		// encode+send of the previous frame, hiding capture latency.
		pc := NewPipelinedCapturer(config.Capturer)
		pc.Start(config.MaxFPS)
		config.Capturer = pc
	}
	if config.Width == 0 {
		config.Width = 1024
	}
	if config.Height == 0 {
		config.Height = 768
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Server{
		config:  config,
		logger:  logger,
		clients: make(map[*ClientConn]struct{}),
		done:    make(chan struct{}),
	}
}

// ListenAndServe starts the VNC server on the given address (e.g. ":5900").
func (s *Server) ListenAndServe(addr string) error {
	if err := checkPortAvailable(addr); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.listener = ln
	s.logger.Info("VNC server listening", "addr", addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.done:
				return nil
			default:
				s.logger.Error("accept error", "error", err)
				continue
			}
		}
		go s.handleConnection(conn)
	}
}

// Close shuts down the server and disconnects all clients.
func (s *Server) Close() error {
	close(s.done)
	if s.listener != nil {
		s.listener.Close()
	}
	s.mu.Lock()
	for c := range s.clients {
		c.Close()
	}
	s.mu.Unlock()
	return nil
}

func (s *Server) addClient(c *ClientConn) {
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) removeClient(c *ClientConn) {
	s.mu.Lock()
	delete(s.clients, c)
	s.mu.Unlock()
}

// ClientConn represents a connected VNC client.
type ClientConn struct {
	conn        net.Conn
	br          *bufio.Reader
	bw          *bufio.Writer
	server      *Server
	pixelFormat PixelFormat
	encodings   []int32

	mu      sync.Mutex
	fbReqCh chan *FramebufferUpdateRequest // async framebuffer updates
	errCh   chan error                     // errors from the fb writer goroutine

	tightEnc   MultiEncoder // lazy-initialized per-connection Tight encoder
	zlibBuf    bytes.Buffer // persistent zlib buffer for Zlib encoding
	zlibWriter  *zlib.Writer // persistent zlib writer for Zlib encoding
	zrleBuf     bytes.Buffer // buffer for one ZRLE rectangle's zlib stream
	zrleWriter  *zlib.Writer // persistent zlib writer for ZRLE encoding

	lastCursor    *CursorImage // last cursor sent to this client
	lastFrameTime time.Time   // last framebuffer update time for FPS limiting
}

func (s *Server) handleConnection(conn net.Conn) {
	if s.config.TLSConfig != nil {
		conn = tls.Server(conn, s.config.TLSConfig)
	}

	c := &ClientConn{
		conn:        conn,
		br:          bufio.NewReader(conn),
		bw:          bufio.NewWriter(conn),
		server:      s,
		pixelFormat: s.config.PixelFormat,
	}
	s.addClient(c)
	defer func() {
		if c.fbReqCh != nil {
			close(c.fbReqCh)
		}
		if c.tightEnc != nil {
			c.tightEnc.Reset()
		}
		s.removeClient(c)
		conn.Close()
		s.logger.Info("client disconnected", "remote_addr", conn.RemoteAddr())
	}()

	s.logger.Info("client connected", "remote_addr", conn.RemoteAddr())

	if err := c.handshake(); err != nil {
		s.logger.Warn("handshake failed", "remote_addr", conn.RemoteAddr(), "error", err)
		return
	}

	// Start async framebuffer writer goroutine
	c.fbReqCh = make(chan *FramebufferUpdateRequest, 1)
	c.errCh = make(chan error, 1)
	go c.framebufferWriter()

	if err := c.serveMessages(); err != nil {
		s.logger.Warn("client error", "remote_addr", conn.RemoteAddr(), "error", err)
	}
}

func (c *ClientConn) handshake() error {
	// Send protocol version
	if _, err := c.bw.WriteString(VersionString3_8); err != nil {
		return fmt.Errorf("write version: %w", err)
	}
	if err := c.bw.Flush(); err != nil {
		return fmt.Errorf("flush version: %w", err)
	}

	// Read client version
	clientVersion := make([]byte, 12)
	if _, err := io.ReadFull(c.br, clientVersion); err != nil {
		return fmt.Errorf("read client version: %w", err)
	}
	c.server.logger.Debug("client version", "version", string(clientVersion[:11]))

	// Send security types
	secTypes := c.server.config.Security
	if err := binary.Write(c.bw, binary.BigEndian, uint8(len(secTypes))); err != nil {
		return fmt.Errorf("write security count: %w", err)
	}
	for _, sec := range secTypes {
		if err := binary.Write(c.bw, binary.BigEndian, sec.Type()); err != nil {
			return fmt.Errorf("write security type: %w", err)
		}
	}
	if err := c.bw.Flush(); err != nil {
		return fmt.Errorf("flush security types: %w", err)
	}

	// Read client security choice
	var chosenType uint8
	if err := binary.Read(c.br, binary.BigEndian, &chosenType); err != nil {
		return fmt.Errorf("read security choice: %w", err)
	}

	// Find matching handler
	var handler SecurityHandler
	for _, sec := range secTypes {
		if sec.Type() == chosenType {
			handler = sec
			break
		}
	}
	if handler == nil {
		return fmt.Errorf("client chose unsupported security type: %d", chosenType)
	}

	upgradedConn, err := handler.Handshake(c.conn, c.br, c.bw)
	if err != nil {
		c.bw.Flush()
		return err
	}
	if upgradedConn != c.conn {
		// TLS upgrade happened (e.g. VeNCrypt); replace conn and buffered I/O so
		// the rest of the session (ServerInit, framebuffer relay, etc.) uses TLS.
		c.conn = upgradedConn
		c.br = bufio.NewReaderSize(upgradedConn, c.br.Size())
		c.bw = bufio.NewWriterSize(upgradedConn, c.bw.Size())
	}
	if err := c.bw.Flush(); err != nil {
		return fmt.Errorf("flush security result: %w", err)
	}

	// Read ClientInit (shared flag)
	var sharedFlag uint8
	if err := binary.Read(c.br, binary.BigEndian, &sharedFlag); err != nil {
		return fmt.Errorf("read client init: %w", err)
	}

	// Send ServerInit
	init := &ServerInit{
		Width:       c.server.config.Width,
		Height:      c.server.config.Height,
		PixelFormat: c.server.config.PixelFormat,
		Name:        c.server.config.Name,
	}
	if err := WriteServerInit(c.bw, init); err != nil {
		return fmt.Errorf("write server init: %w", err)
	}
	return c.bw.Flush()
}

func (c *ClientConn) serveMessages() error {
	for {
		// Check for errors from the framebuffer writer goroutine
		select {
		case err := <-c.errCh:
			return err
		default:
		}

		var msgType uint8
		if err := binary.Read(c.br, binary.BigEndian, &msgType); err != nil {
			return fmt.Errorf("read message type: %w", err)
		}

		switch msgType {
		case MsgSetPixelFormat:
			msg, err := ReadSetPixelFormat(c.br)
			if err != nil {
				return err
			}
			c.mu.Lock()
			c.pixelFormat = msg.PixelFormat
			c.mu.Unlock()

		case MsgSetEncodings:
			encs, err := ReadSetEncodings(c.br)
			if err != nil {
				return err
			}
			c.mu.Lock()
			c.encodings = encs
			c.mu.Unlock()
			c.server.logger.Debug("client encodings", "encodings", encs)

		case MsgFramebufferUpdateRequest:
			req, err := ReadFramebufferUpdateRequest(c.br)
			if err != nil {
				return err
			}
			// Send to async framebuffer writer; drop if it's busy with a previous frame
			select {
			case c.fbReqCh <- req:
			default:
				// Writer is busy — drop this request; client will send another
			}

		case MsgKeyEvent:
			evt, err := ReadKeyEvent(c.br)
			if err != nil {
				return err
			}
			c.server.logger.Debug("key event", "action", boolToAction(evt.DownFlag != 0), "keysym", fmt.Sprintf("0x%04X", evt.Key))
			if c.server.config.Input != nil {
				c.server.config.Input.KeyEvent(evt.DownFlag != 0, evt.Key)
			}

		case MsgPointerEvent:
			evt, err := ReadPointerEvent(c.br)
			if err != nil {
				return err
			}
			c.server.logger.Debug("pointer event", "x", evt.X, "y", evt.Y, "buttons", fmt.Sprintf("0b%08b", evt.ButtonMask))
			if c.server.config.Input != nil {
				c.server.config.Input.PointerEvent(evt.ButtonMask, evt.X, evt.Y)
			}

		case MsgClientCutText:
			text, err := ReadClientCutText(c.br)
			if err != nil {
				return err
			}
			c.server.logger.Info("ClientCutText received from client",
				"text_bytes", len(text),
				"preview", previewText(text, 60))
			if cs, ok := c.server.config.Input.(ClipboardSetter); ok {
				if err := cs.SetClipboard(string(text)); err != nil {
					c.server.logger.Warn("SetClipboard failed", "error", err)
				}
			}

		default:
			return fmt.Errorf("unknown message type: %d", msgType)
		}
	}
}

// framebufferWriter runs in a separate goroutine and processes framebuffer
// update requests. This prevents slow screen captures from blocking the
// message reading loop where input events are processed.
func (c *ClientConn) framebufferWriter() {
	minInterval := time.Second / time.Duration(c.server.config.MaxFPS)

	// Frame timing: track FPS and per-frame timing breakdown.
	var frameCount int64
	var totalCapture, totalEncode, totalSend time.Duration
	lastReport := time.Now()

	for req := range c.fbReqCh {
		// Enforce MaxFPS: sleep if the last frame was sent too recently
		if !c.lastFrameTime.IsZero() {
			elapsed := time.Since(c.lastFrameTime)
			if elapsed < minInterval {
				time.Sleep(minInterval - elapsed)
			}
		}

		frameStart := time.Now()
		captureDur, encodeDur, sendDur, err := c.handleFramebufferRequestTimed(req)
		if err != nil {
			c.errCh <- err
			return
		}
		c.lastFrameTime = time.Now()

		frameCount++
		totalCapture += captureDur
		totalEncode += encodeDur
		totalSend += sendDur

		// Log timing every 5 seconds
		if since := time.Since(lastReport); since >= 5*time.Second {
			fps := float64(frameCount) / since.Seconds()
			avgCapture := time.Duration(0)
			avgEncode := time.Duration(0)
			avgSend := time.Duration(0)
			avgTotal := time.Duration(0)
			if frameCount > 0 {
				avgCapture = totalCapture / time.Duration(frameCount)
				avgEncode = totalEncode / time.Duration(frameCount)
				avgSend = totalSend / time.Duration(frameCount)
				avgTotal = time.Since(frameStart) // just for the log line; use per-frame below
				_ = avgTotal
			}
			c.server.logger.Info("frame timing",
				"fps", fmt.Sprintf("%.1f", fps),
				"avg_capture", avgCapture,
				"avg_encode", avgEncode,
				"avg_send", avgSend,
				"avg_frame", (totalCapture+totalEncode+totalSend)/time.Duration(frameCount),
				"frames", frameCount,
			)
			frameCount = 0
			totalCapture = 0
			totalEncode = 0
			totalSend = 0
			lastReport = time.Now()
		}
	}
}

func boolToAction(down bool) string {
	if down {
		return "down"
	}
	return "up"
}

// bestEncoding returns the best encoding supported by the client, in preference
// order: Tight > Zlib > ZRLE > Raw. Zlib is preferred over ZRLE for compatibility
// with clients that advertise both but only decode ZRLE correctly when it matches
// their exact pixel layout (ZRLE CPIXEL must follow the client's SetPixelFormat).
func (c *ClientConn) bestEncoding() int32 {
	preference := []int32{EncodingTight, EncodingZlib, EncodingZRLE, EncodingRaw}
	for _, pref := range preference {
		if pref == EncodingTight && c.server.config.NewTightEncoder == nil {
			continue
		}
		for _, enc := range c.encodings {
			if enc == pref {
				return pref
			}
		}
	}
	return EncodingRaw
}

// supportsCursor returns true if the client advertised EncodingCursor. Must be called with c.mu held.
func (c *ClientConn) supportsCursor() bool {
	for _, enc := range c.encodings {
		if enc == EncodingCursor {
			return true
		}
	}
	return false
}

// supportsDesktopSize returns true if the client advertised EncodingDesktopSize. Must be called with c.mu held.
func (c *ClientConn) supportsDesktopSize() bool {
	for _, enc := range c.encodings {
		if enc == EncodingDesktopSize {
			return true
		}
	}
	return false
}

// handleFramebufferRequestTimed wraps handleFramebufferRequest with timing
// for capture, encode, and send phases.
func (c *ClientConn) handleFramebufferRequestTimed(req *FramebufferUpdateRequest) (capture, encode, send time.Duration, err error) {
	capturer := c.server.config.Capturer
	if capturer == nil {
		err = c.sendBlankFrame(req)
		return
	}

	t0 := time.Now()
	pixels, stride, captureErr := capturer.Capture()
	capture = time.Since(t0)
	if captureErr != nil {
		err = fmt.Errorf("capture: %w", captureErr)
		return
	}

	// Query dirty rectangles if the capturer supports it.
	// nil  → full-frame change (default when interface not implemented).
	// []   → no change; encodeAndSendFrame will send an empty FBU.
	// [..] → partial update; Tight path will encode only changed regions.
	var dirtyRects []image.Rectangle
	if dc, ok := capturer.(dirtyRectCapturer); ok {
		dirtyRects = dc.LastDirtyRects()
	}

	encode, send, err = c.encodeAndSendFrame(req, pixels, stride, dirtyRects)
	return
}

func (c *ClientConn) encodeAndSendFrame(req *FramebufferUpdateRequest, pixels []byte, stride int, dirtyRects []image.Rectangle) (encode, send time.Duration, err error) {
	capturer := c.server.config.Capturer
	capW, capH := capturer.Bounds()

	bpp := 4
	w := int(req.Width)
	h := int(req.Height)
	rectData := make([]byte, 0, w*h*bpp)

	for row := 0; row < h; row++ {
		srcY := int(req.Y) + row
		srcOffset := srcY*stride + int(req.X)*bpp
		srcEnd := srcOffset + w*bpp
		if srcEnd > len(pixels) {
			break
		}
		rectData = append(rectData, pixels[srcOffset:srcEnd]...)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	var pseudoRects []Rectangle

	// Desktop resize: if the capturer's bounds differ from what we last told the client,
	// send a DesktopSize pseudo-rect so the client can reallocate its framebuffer.
	if c.supportsDesktopSize() {
		if capW != c.server.config.Width || capH != c.server.config.Height {
			c.server.config.Width = capW
			c.server.config.Height = capH
			pseudoRects = append(pseudoRects, EncodeDesktopSizeRect(capW, capH))
			c.server.logger.Info("sending desktop resize", "width", capW, "height", capH)
		}
	}

	// Cursor: send a cursor shape update if the provider has a new cursor image.
	if c.supportsCursor() && c.server.config.CursorProvider != nil {
		cursor := c.server.config.CursorProvider.Cursor()
		if cursor != nil && cursor != c.lastCursor {
			pseudoRects = append(pseudoRects, EncodeCursorRect(cursor, c.pixelFormat, c.server.config.PixelFormat))
			c.lastCursor = cursor
			c.server.logger.Debug("sending cursor update", "width", cursor.Width, "height", cursor.Height, "hotspot_x", cursor.HotspotX, "hotspot_y", cursor.HotspotY)
		}
	}

	bestEnc := c.bestEncoding()
	c.server.logger.Debug("best encoding", "encoding", bestEnc, "encodings", c.encodings, "tight_available", c.server.config.NewTightEncoder != nil)

	// If dirtyRects is non-nil and empty, the capturer signalled no change.
	// Send an empty FBU (RFC 6143 §7.6.1 permits zero rectangles) so the client
	// can issue its next request. Pseudo-rects (cursor, desktop resize) are still
	// sent when present so they are never dropped.
	if dirtyRects != nil && len(dirtyRects) == 0 {
		encode = 0
		sendStart := time.Now()
		if writeErr := WriteFramebufferUpdate(c.bw, pseudoRects); writeErr != nil {
			err = writeErr
			return
		}
		err = c.bw.Flush()
		send = time.Since(sendStart)
		return
	}

	encodeStart := time.Now()

	switch bestEnc {
	case EncodingTight:
		if c.tightEnc == nil {
			c.tightEnc = c.server.config.NewTightEncoder()
		}
		var rects []Rectangle
		if len(dirtyRects) > 0 {
			// Dirty-rect path: encode only the changed regions intersected with req.
			reqRect := image.Rect(int(req.X), int(req.Y), int(req.X)+int(req.Width), int(req.Y)+int(req.Height))
			for _, dr := range dirtyRects {
				ix := dr.Intersect(reqRect)
				if ix.Empty() {
					continue
				}
				tileRects, encErr := c.tightEnc.EncodeMulti(
					uint16(ix.Min.X), uint16(ix.Min.Y),
					uint16(ix.Dx()), uint16(ix.Dy()),
					pixels, stride)
				if encErr != nil {
					err = fmt.Errorf("tight encode: %w", encErr)
					return
				}
				rects = append(rects, tileRects...)
			}
		} else {
			// Full-frame path (dirtyRects == nil: full change or no dirty-rect support).
			var encErr error
			rects, encErr = c.tightEnc.EncodeMulti(req.X, req.Y, req.Width, req.Height, pixels, stride)
			if encErr != nil {
				err = fmt.Errorf("tight encode: %w", encErr)
				return
			}
		}
		rects = append(pseudoRects, rects...)
		encode = time.Since(encodeStart)
		sendStart := time.Now()
		if writeErr := WriteFramebufferUpdate(c.bw, rects); writeErr != nil {
			err = writeErr
			return
		}
		err = c.bw.Flush()
		send = time.Since(sendStart)
		return

	case EncodingZRLE:
		// Convert pixels to the client's pixel format first, then extract CPIXELs.
		clientBpp := int(c.pixelFormat.BitsPerPixel) / 8
		cpixelSize, cpixelOff := zrleCPixel(c.pixelFormat)

		// Build a tightly-packed pixel buffer in client format for the requested region.
		clientPixels := make([]byte, w*h*clientBpp)
		for row := 0; row < h; row++ {
			srcY := int(req.Y) + row
			srcOffset := srcY*stride + int(req.X)*4
			srcEnd := srcOffset + w*4
			if srcEnd > len(pixels) {
				break
			}
			copy(clientPixels[row*w*4:(row+1)*w*4], pixels[srcOffset:srcEnd])
		}
		clientPixels = ConvertPixels(c.pixelFormat, c.server.config.PixelFormat, clientPixels, w, h)

		// ZRLE uses a single persistent zlib stream per connection (RFC 6143 §7.7.6).
		c.zrleBuf.Reset()
		if c.zrleWriter == nil {
			c.zrleWriter = zlib.NewWriter(&c.zrleBuf)
		}

		tileSize := 64
		for tileY := 0; tileY < h; tileY += tileSize {
			tileH := min(tileSize, h-tileY)
			for tileX := 0; tileX < w; tileX += tileSize {
				tileW := min(tileSize, w-tileX)

				// Extract tile CPIXELs from the converted pixel buffer.
				tilePixels := make([]byte, tileW*tileH*cpixelSize)
				for row := 0; row < tileH; row++ {
					for col := 0; col < tileW; col++ {
						srcOff := ((tileY+row)*w + (tileX + col)) * clientBpp
						dstOff := (row*tileW + col) * cpixelSize
						copy(tilePixels[dstOff:dstOff+cpixelSize], clientPixels[srcOff+cpixelOff:srcOff+cpixelOff+cpixelSize])
					}
				}

				// Check if solid tile
				solid := true
				for i := cpixelSize; i < len(tilePixels); i += cpixelSize {
					if !bytesEqual(tilePixels[i:i+cpixelSize], tilePixels[0:cpixelSize]) {
						solid = false
						break
					}
				}

				if solid {
					c.zrleWriter.Write([]byte{1}) // solid subtype
					c.zrleWriter.Write(tilePixels[0:cpixelSize])
				} else {
					c.zrleWriter.Write([]byte{0}) // raw subtype
					c.zrleWriter.Write(tilePixels)
				}
			}
		}

		if flushErr := c.zrleWriter.Flush(); flushErr != nil {
			err = fmt.Errorf("zrle zlib flush: %w", flushErr)
			return
		}

		compressedLen := c.zrleBuf.Len()
		data := make([]byte, 4+compressedLen)
		binary.BigEndian.PutUint32(data[0:4], uint32(compressedLen))
		copy(data[4:], c.zrleBuf.Bytes())

		rect := Rectangle{
			Header: RectHeader{
				X:        req.X,
				Y:        req.Y,
				Width:    req.Width,
				Height:   req.Height,
				Encoding: EncodingZRLE,
			},
			Data: data,
		}
		allRects := append(pseudoRects, rect)
		encode = time.Since(encodeStart)
		sendStart := time.Now()
		if writeErr := WriteFramebufferUpdate(c.bw, allRects); writeErr != nil {
			err = writeErr
			return
		}
		err = c.bw.Flush()
		send = time.Since(sendStart)
		return

	case EncodingZlib:
		rectData = ConvertPixels(c.pixelFormat, c.server.config.PixelFormat, rectData, w, h)

		c.zlibBuf.Reset()
		if c.zlibWriter == nil {
			c.zlibWriter = zlib.NewWriter(&c.zlibBuf)
		}
		if _, zlibErr := c.zlibWriter.Write(rectData); zlibErr != nil {
			err = fmt.Errorf("zlib write: %w", zlibErr)
			return
		}
		if zlibErr := c.zlibWriter.Flush(); zlibErr != nil {
			err = fmt.Errorf("zlib flush: %w", zlibErr)
			return
		}

		compressedLen := c.zlibBuf.Len()
		data := make([]byte, 4+compressedLen)
		binary.BigEndian.PutUint32(data[0:4], uint32(compressedLen))
		copy(data[4:], c.zlibBuf.Bytes())

		rect := Rectangle{
			Header: RectHeader{
				X:        req.X,
				Y:        req.Y,
				Width:    req.Width,
				Height:   req.Height,
				Encoding: EncodingZlib,
			},
			Data: data,
		}
		allRects := append(pseudoRects, rect)
		encode = time.Since(encodeStart)
		sendStart := time.Now()
		if writeErr := WriteFramebufferUpdate(c.bw, allRects); writeErr != nil {
			err = writeErr
			return
		}
		err = c.bw.Flush()
		send = time.Since(sendStart)
		return

	default:
		rectData = ConvertPixels(c.pixelFormat, c.server.config.PixelFormat, rectData, w, h)

		rect := Rectangle{
			Header: RectHeader{
				X:        req.X,
				Y:        req.Y,
				Width:    req.Width,
				Height:   req.Height,
				Encoding: EncodingRaw,
			},
			Data: rectData,
		}

		allRects := append(pseudoRects, rect)
		encode = time.Since(encodeStart)
		sendStart := time.Now()
		if writeErr := WriteFramebufferUpdate(c.bw, allRects); writeErr != nil {
			err = writeErr
			return
		}
		err = c.bw.Flush()
		send = time.Since(sendStart)
		return
	}
}

// NotifyDesktopResize notifies all connected clients that support the DesktopSize
// pseudo-encoding about a screen resolution change. Clients that don't support
// the encoding are unaffected — they continue at the old resolution.
func (s *Server) NotifyDesktopResize(width, height uint16) {
	s.mu.Lock()
	s.config.Width = width
	s.config.Height = height
	clients := make([]*ClientConn, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	for _, c := range clients {
		c.mu.Lock()
		if c.supportsDesktopSize() {
			rect := EncodeDesktopSizeRect(width, height)
			if err := WriteFramebufferUpdate(c.bw, []Rectangle{rect}); err != nil {
				c.mu.Unlock()
				s.logger.Warn("desktop resize notify error", "remote_addr", c.conn.RemoteAddr(), "error", err)
				continue
			}
			if err := c.bw.Flush(); err != nil {
				c.mu.Unlock()
				s.logger.Warn("desktop resize flush error", "remote_addr", c.conn.RemoteAddr(), "error", err)
				continue
			}
			s.logger.Info("notified desktop resize", "remote_addr", c.conn.RemoteAddr(), "width", width, "height", height)
		}
		c.mu.Unlock()
	}
}

// SendCursorUpdate sends a cursor shape update to all connected clients that
// support the Cursor pseudo-encoding.
func (s *Server) SendCursorUpdate(cursor *CursorImage) {
	s.mu.Lock()
	clients := make([]*ClientConn, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	for _, c := range clients {
		c.mu.Lock()
		if c.supportsCursor() {
			rect := EncodeCursorRect(cursor, c.pixelFormat, s.config.PixelFormat)
			if err := WriteFramebufferUpdate(c.bw, []Rectangle{rect}); err != nil {
				c.mu.Unlock()
				s.logger.Warn("cursor update error", "remote_addr", c.conn.RemoteAddr(), "error", err)
				continue
			}
			if err := c.bw.Flush(); err != nil {
				c.mu.Unlock()
				s.logger.Warn("cursor flush error", "remote_addr", c.conn.RemoteAddr(), "error", err)
				continue
			}
			c.lastCursor = cursor
		}
		c.mu.Unlock()
	}
}

// SendClipboard broadcasts a ServerCutText message to all connected clients.
// Call this whenever the server-side clipboard changes (e.g. after detecting
// a copy operation on the host OS).
func (s *Server) SendClipboard(text string) {
	s.mu.Lock()
	clients := make([]*ClientConn, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	s.logger.Info("SendClipboard broadcasting to clients",
		"num_clients", len(clients),
		"text_bytes", len(text),
		"preview", previewText(text, 60))

	for _, c := range clients {
		c.mu.Lock()
		if err := WriteServerCutText(c.bw, text); err != nil {
			c.mu.Unlock()
			s.logger.Warn("ServerCutText send error", "remote_addr", c.conn.RemoteAddr(), "error", err)
			continue
		}
		if err := c.bw.Flush(); err != nil {
			c.mu.Unlock()
			s.logger.Warn("ServerCutText flush error", "remote_addr", c.conn.RemoteAddr(), "error", err)
			continue
		}
		c.mu.Unlock()
		s.logger.Debug("ServerCutText sent to client", "remote_addr", c.conn.RemoteAddr())
	}
}

func (c *ClientConn) sendBlankFrame(req *FramebufferUpdateRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	bpp := int(c.pixelFormat.BitsPerPixel) / 8
	data := make([]byte, int(req.Width)*int(req.Height)*bpp)

	rect := Rectangle{
		Header: RectHeader{
			X:        req.X,
			Y:        req.Y,
			Width:    req.Width,
			Height:   req.Height,
			Encoding: EncodingRaw,
		},
		Data: data,
	}

	if err := WriteFramebufferUpdate(c.bw, []Rectangle{rect}); err != nil {
		return err
	}
	return c.bw.Flush()
}

// zrleCPixel returns the CPIXEL byte size and the offset within a full pixel
// from which to copy those bytes, given the client's pixel format.
// Per RFC 6143 §7.7.6, if bpp=32 and the significant bits fit in the least or
// most significant 24 bits, CPIXEL is 3 bytes; otherwise it equals bpp/8.
func zrleCPixel(pf PixelFormat) (size int, offset int) {
	if pf.BitsPerPixel == 32 && pf.Depth <= 24 {
		rEnd := uint32(pf.RedShift) + bitLen(pf.RedMax)
		gEnd := uint32(pf.GreenShift) + bitLen(pf.GreenMax)
		bEnd := uint32(pf.BlueShift) + bitLen(pf.BlueMax)
		maxEnd := max(rEnd, gEnd, bEnd)
		minShift := min(uint32(pf.RedShift), uint32(pf.GreenShift), uint32(pf.BlueShift))

		if maxEnd <= 24 {
			// Significant bits in least significant 24 bits.
			if pf.BigEndian != 0 {
				return 3, 1 // skip MSB
			}
			return 3, 0 // take first 3 bytes
		}
		if minShift >= 8 {
			// Significant bits in most significant 24 bits.
			if pf.BigEndian != 0 {
				return 3, 0 // take first 3 bytes
			}
			return 3, 1 // skip LSB
		}
	}
	return int(pf.BitsPerPixel) / 8, 0
}

// bitLen returns the number of bits needed to represent v.
func bitLen(v uint16) uint32 {
	n := uint32(0)
	for x := uint32(v); x > 0; x >>= 1 {
		n++
	}
	return n
}

// bytesEqual returns true if two byte slices are equal.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Close closes the client connection.
func (c *ClientConn) Close() error {
	return c.conn.Close()
}

// noneSecurity is the default no-auth handler.
type noneSecurity struct{}

func (n *noneSecurity) Type() uint8 { return SecurityNone }
func (n *noneSecurity) Handshake(conn net.Conn, br *bufio.Reader, bw *bufio.Writer) (net.Conn, error) {
	return conn, binary.Write(bw, binary.BigEndian, uint32(0))
}

// previewText returns up to maxLen characters of s for use in log messages.
func previewText(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "…"
}
