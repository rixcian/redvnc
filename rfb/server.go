package rfb

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
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
}

// Server is an RFB protocol server that accepts VNC client connections.
type Server struct {
	config   ServerConfig
	listener net.Listener

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
	if config.Capturer != nil {
		config.Width, config.Height = config.Capturer.Bounds()
	}
	if config.Width == 0 {
		config.Width = 1024
	}
	if config.Height == 0 {
		config.Height = 768
	}

	return &Server{
		config:  config,
		clients: make(map[*ClientConn]struct{}),
		done:    make(chan struct{}),
	}
}

// ListenAndServe starts the VNC server on the given address (e.g. ":5900").
func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.listener = ln
	log.Printf("VNC server listening on %s", addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.done:
				return nil
			default:
				log.Printf("accept error: %v", err)
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
}

func (s *Server) handleConnection(conn net.Conn) {
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
		log.Printf("client disconnected: %s", conn.RemoteAddr())
	}()

	log.Printf("client connected: %s", conn.RemoteAddr())

	if err := c.handshake(); err != nil {
		log.Printf("handshake failed for %s: %v", conn.RemoteAddr(), err)
		return
	}

	// Start async framebuffer writer goroutine
	c.fbReqCh = make(chan *FramebufferUpdateRequest, 1)
	c.errCh = make(chan error, 1)
	go c.framebufferWriter()

	if err := c.serveMessages(); err != nil {
		log.Printf("client %s error: %v", conn.RemoteAddr(), err)
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
	log.Printf("client version: %s", string(clientVersion[:11]))

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
			log.Printf("client encodings: %v", encs)

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
			log.Printf("[input] key %s keysym=0x%04X", boolToAction(evt.DownFlag != 0), evt.Key)
			if c.server.config.Input != nil {
				c.server.config.Input.KeyEvent(evt.DownFlag != 0, evt.Key)
			}

		case MsgPointerEvent:
			evt, err := ReadPointerEvent(c.br)
			if err != nil {
				return err
			}
			log.Printf("[input] pointer x=%d y=%d buttons=0b%08b", evt.X, evt.Y, evt.ButtonMask)
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
	for req := range c.fbReqCh {
		if err := c.handleFramebufferRequest(req); err != nil {
			c.errCh <- err
			return
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
// order: Tight > Zlib > Raw. Must be called with c.mu held.
func (c *ClientConn) bestEncoding() int32 {
	preference := []int32{EncodingTight, EncodingZlib, EncodingRaw}
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

	bestEnc := c.bestEncoding()
	log.Printf("best encoding: %d (encodings=%v, tightFactory=%v)", bestEnc, c.encodings, c.server.config.NewTightEncoder != nil)

	switch bestEnc {
	case EncodingTight:
		if c.tightEnc == nil {
			c.tightEnc = c.server.config.NewTightEncoder()
		}
		rects, err := c.tightEnc.EncodeMulti(req.X, req.Y, req.Width, req.Height, pixels, stride)
		if err != nil {
			return fmt.Errorf("tight encode: %w", err)
		}
		if err := WriteFramebufferUpdate(c.bw, rects); err != nil {
			return err
		}
		return c.bw.Flush()

	case EncodingZlib:
		rectData = ConvertPixels(c.pixelFormat, c.server.config.PixelFormat, rectData, w, h)

		c.zlibBuf.Reset()
		if c.zlibWriter == nil {
			c.zlibWriter = zlib.NewWriter(&c.zlibBuf)
		} else {
			c.zlibWriter.Reset(&c.zlibBuf)
		}
		if _, err := c.zlibWriter.Write(rectData); err != nil {
			return fmt.Errorf("zlib write: %w", err)
		}
		if err := c.zlibWriter.Close(); err != nil {
			return fmt.Errorf("zlib close: %w", err)
		}
		c.zlibWriter = nil

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
		if err := WriteFramebufferUpdate(c.bw, []Rectangle{rect}); err != nil {
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

		if err := WriteFramebufferUpdate(c.bw, []Rectangle{rect}); err != nil {
			return err
		}
		return c.bw.Flush()
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
