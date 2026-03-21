package rfb

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/tls"
	"encoding/binary"
	"fmt"
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

// InputHandler processes keyboard and pointer events from VNC clients.
type InputHandler interface {
	// KeyEvent handles a key press or release.
	KeyEvent(down bool, key uint32)
	// PointerEvent handles a pointer movement or button click.
	PointerEvent(buttonMask uint8, x, y uint16)
}

// CursorProvider supplies cursor image data to the server.
// The server calls Cursor() when building framebuffer updates for clients
// that support the Cursor pseudo-encoding.
type CursorProvider interface {
	// Cursor returns the current cursor image, or nil if no cursor update is needed.
	Cursor() *CursorImage
}

// SecurityHandler performs the security handshake with a client.
type SecurityHandler interface {
	Type() uint8
	Handshake(rw io.ReadWriter) error
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
	zlibWriter *zlib.Writer // persistent zlib writer
	zrleBuf    bytes.Buffer // persistent zlib buffer for ZRLE encoding
	zrleWriter *zlib.Writer // persistent zlib writer for ZRLE

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

	// Create a ReadWriter combining buffered reader/writer for the handshake
	rw := &bufReadWriter{r: c.br, w: c.bw}
	if err := handler.Handshake(rw); err != nil {
		c.bw.Flush()
		return err
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
			_, err := ReadClientCutText(c.br)
			if err != nil {
				return err
			}
			// Clipboard handling can be extended later

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

	for req := range c.fbReqCh {
		// Enforce MaxFPS: sleep if the last frame was sent too recently
		if !c.lastFrameTime.IsZero() {
			elapsed := time.Since(c.lastFrameTime)
			if elapsed < minInterval {
				time.Sleep(minInterval - elapsed)
			}
		}

		if err := c.handleFramebufferRequest(req); err != nil {
			c.errCh <- err
			return
		}
		c.lastFrameTime = time.Now()
	}
}

func boolToAction(down bool) string {
	if down {
		return "down"
	}
	return "up"
}

// bestEncoding returns the best encoding supported by the client, in preference
// order: Tight > ZRLE > Zlib > Raw. Must be called with c.mu held.
func (c *ClientConn) bestEncoding() int32 {
	preference := []int32{EncodingTight, EncodingZRLE, EncodingZlib, EncodingRaw}
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

func (c *ClientConn) handleFramebufferRequest(req *FramebufferUpdateRequest) error {
	capturer := c.server.config.Capturer
	if capturer == nil {
		// Send a blank rectangle
		return c.sendBlankFrame(req)
	}

	pixels, stride, err := capturer.Capture()
	if err != nil {
		return fmt.Errorf("capture: %w", err)
	}

	// Check if the screen has been resized since the last frame.
	capW, capH := capturer.Bounds()

	bpp := 4 // 32-bit pixels
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

	// Collect pseudo-encoding rectangles to prepend to the update.
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

	switch bestEnc {
	case EncodingTight:
		if c.tightEnc == nil {
			c.tightEnc = c.server.config.NewTightEncoder()
		}
		rects, err := c.tightEnc.EncodeMulti(req.X, req.Y, req.Width, req.Height, pixels, stride)
		if err != nil {
			return fmt.Errorf("tight encode: %w", err)
		}
		rects = append(pseudoRects, rects...)
		if err := WriteFramebufferUpdate(c.bw, rects); err != nil {
			return err
		}
		return c.bw.Flush()

	case EncodingZRLE:
		// ZRLE uses CPIXEL (3-byte BGR), so we pass raw BGRA pixels and the
		// encoder handles the conversion internally.
		c.zrleBuf.Reset()
		if c.zrleWriter == nil {
			c.zrleWriter = zlib.NewWriter(&c.zrleBuf)
		}

		bpp := 4
		tileSize := 64
		for tileY := int(req.Y); tileY < int(req.Y)+h; tileY += tileSize {
			tileH := min(tileSize, int(req.Y)+h-tileY)
			for tileX := int(req.X); tileX < int(req.X)+w; tileX += tileSize {
				tileW := min(tileSize, int(req.X)+w-tileX)

				// Extract tile pixels as CPIXEL (3 bytes)
				tilePixels := make([]byte, tileW*tileH*3)
				for row := 0; row < tileH; row++ {
					srcY := tileY + row
					for col := 0; col < tileW; col++ {
						srcX := tileX + col
						srcOff := srcY*stride + srcX*bpp
						dstOff := (row*tileW + col) * 3
						tilePixels[dstOff] = pixels[srcOff]     // B
						tilePixels[dstOff+1] = pixels[srcOff+1] // G
						tilePixels[dstOff+2] = pixels[srcOff+2] // R
					}
				}

				// Check if solid tile
				solid := true
				for i := 3; i < len(tilePixels); i += 3 {
					if tilePixels[i] != tilePixels[0] || tilePixels[i+1] != tilePixels[1] || tilePixels[i+2] != tilePixels[2] {
						solid = false
						break
					}
				}

				if solid {
					c.zrleWriter.Write([]byte{1}) // solid subtype
					c.zrleWriter.Write(tilePixels[0:3])
				} else {
					c.zrleWriter.Write([]byte{0}) // raw subtype
					c.zrleWriter.Write(tilePixels)
				}
			}
		}

		if err := c.zrleWriter.Flush(); err != nil {
			return fmt.Errorf("zrle flush: %w", err)
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
		if err := WriteFramebufferUpdate(c.bw, allRects); err != nil {
			return err
		}
		return c.bw.Flush()

	case EncodingZlib:
		rectData = ConvertPixels(c.pixelFormat, c.server.config.PixelFormat, rectData, w, h)

		// The zlib stream must persist across frames — the client maintains a
		// single decompressor. Reset only the buffer (not the writer) so the
		// dictionary is preserved, then Flush (Z_SYNC_FLUSH) instead of Close.
		c.zlibBuf.Reset()
		if c.zlibWriter == nil {
			c.zlibWriter = zlib.NewWriter(&c.zlibBuf)
		}
		if _, err := c.zlibWriter.Write(rectData); err != nil {
			return fmt.Errorf("zlib write: %w", err)
		}
		if err := c.zlibWriter.Flush(); err != nil {
			return fmt.Errorf("zlib flush: %w", err)
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
		if err := WriteFramebufferUpdate(c.bw, allRects); err != nil {
			return err
		}
		return c.bw.Flush()

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
		if err := WriteFramebufferUpdate(c.bw, allRects); err != nil {
			return err
		}
		return c.bw.Flush()
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

// Close closes the client connection.
func (c *ClientConn) Close() error {
	return c.conn.Close()
}

// noneSecurity is the default no-auth handler.
type noneSecurity struct{}

func (n *noneSecurity) Type() uint8 { return SecurityNone }
func (n *noneSecurity) Handshake(rw io.ReadWriter) error {
	return binary.Write(rw, binary.BigEndian, uint32(0))
}

// bufReadWriter combines a buffered reader and writer into an io.ReadWriter.
type bufReadWriter struct {
	r io.Reader
	w *bufio.Writer
}

func (brw *bufReadWriter) Read(p []byte) (int, error) {
	return brw.r.Read(p)
}

func (brw *bufReadWriter) Write(p []byte) (int, error) {
	n, err := brw.w.Write(p)
	if err != nil {
		return n, err
	}
	return n, brw.w.Flush()
}
