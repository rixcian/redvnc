package wsproxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/rixcian/redvnc/rfb"
	"github.com/rixcian/redvnc/rfb/security"
	"nhooyr.io/websocket"
)

// Extension message types.
const (
	ExtSessionInit     uint8 = 128
	ExtClipboardSet    uint8 = 129
	ExtClipboardUpdate uint8 = 130
	ExtUploadBegin     uint8 = 131
	ExtUploadChunk     uint8 = 132
	ExtUploadEnd       uint8 = 133
	ExtUploadStatus    uint8 = 134
	ExtUploadCancel    uint8 = 135
)

// Proxy manages the WebSocket ↔ TCP relay for a single session.
type Proxy struct {
	session   *Session
	server    *Server
	rfbReader *rfbReader
	authType  uint8 // RFB security type used (1=None, 2=VNCAuth)
}

// NewProxy creates a new proxy for the given session.
func NewProxy(session *Session, server *Server) *Proxy {
	return &Proxy{
		session: session,
		server:  server,
	}
}

// Run starts the proxy relay. It blocks until the session ends.
func (p *Proxy) Run(ctx context.Context) {
	// Connect to VNC server
	tcpConn, err := net.DialTimeout("tcp", p.session.Target, 10*time.Second)
	if err != nil {
		p.server.logger.Error("failed to connect to VNC server", "session_id", p.session.ID, "target", p.session.Target, "error", err)
		p.session.WSConn.Close(websocket.StatusInternalError, "failed to connect to VNC server")
		return
	}
	p.session.TCPConn = tcpConn
	defer tcpConn.Close()

	// Create a buffered reader that is shared between handshake and relay
	// to avoid losing bytes buffered during the handshake.
	br := bufio.NewReaderSize(tcpConn, 256*1024)

	// Perform RFB handshake with VNC server
	serverInit, err := p.performHandshake(tcpConn, br)
	if err != nil {
		p.server.logger.Warn("handshake failed", "session_id", p.session.ID, "error", err)
		// Record auth failure for rate limiting
		clientIP := extractIP(p.session.ClientAddr)
		p.server.rateLimiter.RecordFailure(clientIP)
		p.session.WSConn.Close(websocket.StatusInternalError, "VNC handshake failed")
		return
	}

	// Handshake succeeded — clear any rate limit failures
	clientIP := extractIP(p.session.ClientAddr)
	p.server.rateLimiter.ClearFailures(clientIP)

	p.server.logger.Info("handshake complete", "session_id", p.session.ID, "width", serverInit.Width, "height", serverInit.Height, "name", serverInit.Name)

	// Create RFB message reader using the server's initial pixel format.
	// This is updated when the client sends SetPixelFormat.
	pf := serverInit.PixelFormat
	p.rfbReader = newRFBReader(br, pf.BitsPerPixel, pf.Depth, pf.TrueColour)

	// Send SessionInit extension message to browser
	if err := p.sendSessionInit(ctx, serverInit); err != nil {
		p.server.logger.Error("failed to send session init", "session_id", p.session.ID, "error", err)
		p.session.WSConn.Close(websocket.StatusInternalError, "failed to send session init")
		return
	}

	// Enter relay mode
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// VNC → Browser relay
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		p.relayVNCToBrowser(ctx)
	}()

	// Browser → VNC relay
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		p.relayBrowserToVNC(ctx)
	}()

	wg.Wait()
}

// performHandshake performs the RFB handshake with the VNC server.
func (p *Proxy) performHandshake(conn net.Conn, br *bufio.Reader) (*rfb.ServerInit, error) {
	bw := bufio.NewWriter(conn)

	// Read server version
	serverVersion := make([]byte, 12)
	if _, err := io.ReadFull(br, serverVersion); err != nil {
		return nil, fmt.Errorf("read server version: %w", err)
	}

	// Send our version (3.8)
	if _, err := bw.WriteString(rfb.VersionString3_8); err != nil {
		return nil, fmt.Errorf("write version: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return nil, fmt.Errorf("flush version: %w", err)
	}

	// Read security types
	var numTypes uint8
	if err := binary.Read(br, binary.BigEndian, &numTypes); err != nil {
		return nil, fmt.Errorf("read security type count: %w", err)
	}
	if numTypes == 0 {
		// Server refused connection, read reason
		var reasonLen uint32
		if err := binary.Read(br, binary.BigEndian, &reasonLen); err != nil {
			return nil, fmt.Errorf("read failure reason length: %w", err)
		}
		reason := make([]byte, reasonLen)
		if _, err := io.ReadFull(br, reason); err != nil {
			return nil, fmt.Errorf("read failure reason: %w", err)
		}
		return nil, fmt.Errorf("server refused connection: %s", string(reason))
	}

	secTypes := make([]uint8, numTypes)
	for i := range secTypes {
		if err := binary.Read(br, binary.BigEndian, &secTypes[i]); err != nil {
			return nil, fmt.Errorf("read security type: %w", err)
		}
	}

	// Choose security type
	chosenType := chooseSecurityType(secTypes, p.session.Password)
	p.authType = chosenType
	if err := binary.Write(bw, binary.BigEndian, chosenType); err != nil {
		return nil, fmt.Errorf("write security choice: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return nil, fmt.Errorf("flush security choice: %w", err)
	}

	// Perform security handshake
	// We use a combined reader/writer that works through the buffered streams
	rw := &bufReadWriter{br: br, bw: bw}
	switch chosenType {
	case rfb.SecurityNone:
		// Nothing to do before reading result
	case rfb.SecurityVNCAuth:
		if err := security.VNCAuthClient(rw, p.session.Password); err != nil {
			return nil, fmt.Errorf("vnc auth: %w", err)
		}
		if err := bw.Flush(); err != nil {
			return nil, fmt.Errorf("flush vnc auth: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported security type: %d", chosenType)
	}

	// Read SecurityResult
	var secResult uint32
	if err := binary.Read(br, binary.BigEndian, &secResult); err != nil {
		return nil, fmt.Errorf("read security result: %w", err)
	}
	if secResult != 0 {
		var reasonLen uint32
		if err := binary.Read(br, binary.BigEndian, &reasonLen); err != nil {
			return nil, fmt.Errorf("authentication failed (cannot read reason)")
		}
		reason := make([]byte, reasonLen)
		if _, err := io.ReadFull(br, reason); err != nil {
			return nil, fmt.Errorf("authentication failed (cannot read reason text)")
		}
		return nil, fmt.Errorf("authentication failed: %s", string(reason))
	}

	// Send ClientInit with shared=1
	if err := binary.Write(bw, binary.BigEndian, uint8(1)); err != nil {
		return nil, fmt.Errorf("write client init: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return nil, fmt.Errorf("flush client init: %w", err)
	}

	// Read ServerInit
	init := &rfb.ServerInit{}
	if err := binary.Read(br, binary.BigEndian, &init.Width); err != nil {
		return nil, fmt.Errorf("read width: %w", err)
	}
	if err := binary.Read(br, binary.BigEndian, &init.Height); err != nil {
		return nil, fmt.Errorf("read height: %w", err)
	}
	if err := binary.Read(br, binary.BigEndian, &init.PixelFormat); err != nil {
		return nil, fmt.Errorf("read pixel format: %w", err)
	}
	var nameLen uint32
	if err := binary.Read(br, binary.BigEndian, &nameLen); err != nil {
		return nil, fmt.Errorf("read name length: %w", err)
	}
	if nameLen > 4096 {
		return nil, fmt.Errorf("server name too long: %d", nameLen)
	}
	nameBytes := make([]byte, nameLen)
	if _, err := io.ReadFull(br, nameBytes); err != nil {
		return nil, fmt.Errorf("read name: %w", err)
	}
	init.NameLength = nameLen
	init.Name = string(nameBytes)

	return init, nil
}

// sendSessionInit sends the session init extension message to the browser.
func (p *Proxy) sendSessionInit(ctx context.Context, init *rfb.ServerInit) error {
	nameBytes := []byte(init.Name)
	// payload: width(2) + height(2) + pixelFormat(16) + nameLength(4) + name(n) + authType(1)
	payloadLen := 2 + 2 + 16 + 4 + len(nameBytes) + 1

	buf := make([]byte, 5+payloadLen)
	buf[0] = ExtSessionInit
	binary.BigEndian.PutUint32(buf[1:5], uint32(payloadLen))
	binary.BigEndian.PutUint16(buf[5:7], init.Width)
	binary.BigEndian.PutUint16(buf[7:9], init.Height)

	// Encode PixelFormat (16 bytes)
	pf := init.PixelFormat
	off := 9
	buf[off] = pf.BitsPerPixel
	buf[off+1] = pf.Depth
	buf[off+2] = pf.BigEndian
	buf[off+3] = pf.TrueColour
	binary.BigEndian.PutUint16(buf[off+4:off+6], pf.RedMax)
	binary.BigEndian.PutUint16(buf[off+6:off+8], pf.GreenMax)
	binary.BigEndian.PutUint16(buf[off+8:off+10], pf.BlueMax)
	buf[off+10] = pf.RedShift
	buf[off+11] = pf.GreenShift
	buf[off+12] = pf.BlueShift
	// 3 bytes padding (already zeroed)

	off += 16
	binary.BigEndian.PutUint32(buf[off:off+4], uint32(len(nameBytes)))
	off += 4
	copy(buf[off:], nameBytes)
	off += len(nameBytes)
	buf[off] = p.authType

	return p.session.WSConn.Write(ctx, websocket.MessageBinary, buf)
}

// relayVNCToBrowser reads complete RFB messages from the TCP connection and
// writes each as a single WebSocket message. This ensures the browser always
// receives properly framed RFB messages regardless of TCP chunking.
func (p *Proxy) relayVNCToBrowser(ctx context.Context) {
	for {
		msg, err := p.rfbReader.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				p.server.logger.Warn("VNC read error", "session_id", p.session.ID, "error", err)
			}
			return
		}

		p.session.TouchActivity()
		p.session.BytesToClient.Add(int64(len(msg)))

		// Log clipboard messages from the VNC server so clipboard sync
		// issues can be diagnosed without code changes on the client side.
		if len(msg) > 0 && msg[0] == 3 {
			textLen := 0
			if len(msg) >= 8 {
				textLen = int(binary.BigEndian.Uint32(msg[4:8]))
			}
			p.server.logger.Debug("ServerCutText received from VNC",
				"session_id", p.session.ID,
				"text_bytes", textLen)
		}

		if err := p.session.WSConn.Write(ctx, websocket.MessageBinary, msg); err != nil {
			if ctx.Err() == nil {
				p.server.logger.Warn("WS write error", "session_id", p.session.ID, "error", err)
			}
			return
		}
	}
}

// relayBrowserToVNC reads from the WebSocket and processes messages.
func (p *Proxy) relayBrowserToVNC(ctx context.Context) {
	for {
		msgType, data, err := p.session.WSConn.Read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				p.server.logger.Warn("WS read error", "session_id", p.session.ID, "error", err)
			}
			return
		}

		// Only accept binary messages
		if msgType != websocket.MessageBinary {
			p.server.logger.Warn("rejecting non-binary message", "session_id", p.session.ID)
			continue
		}

		if len(data) == 0 {
			continue
		}

		p.session.TouchActivity()
		p.session.BytesFromClient.Add(int64(len(data)))

		// Check message type byte
		rfbType := data[0]

		if rfbType >= 128 {
			// Extension message - handle locally
			p.handleExtensionMessage(ctx, rfbType, data)
		} else {
			// Intercept SetPixelFormat (client msg type 0) to track the pixel
			// format for correct RFB message framing on the VNC→Browser path.
			if rfbType == 0 && len(data) >= 20 {
				bpp := data[4]
				depth := data[5]
				trueColour := data[7]
				p.rfbReader.UpdatePixelFormat(bpp, depth, trueColour)
			}

			// Standard RFB message - relay to VNC server
			if _, err := p.session.TCPConn.Write(data); err != nil {
				if ctx.Err() == nil {
					p.server.logger.Warn("TCP write error", "session_id", p.session.ID, "error", err)
				}
				return
			}
		}
	}
}

// handleExtensionMessage processes extension messages from the browser.
func (p *Proxy) handleExtensionMessage(ctx context.Context, msgType uint8, data []byte) {
	switch msgType {
	case ExtClipboardSet:
		p.handleClipboardSet(ctx, data)
	case ExtUploadBegin:
		p.handleUploadBegin(ctx, data)
	case ExtUploadChunk:
		p.handleUploadChunk(ctx, data)
	case ExtUploadEnd:
		p.handleUploadEnd(ctx, data)
	case ExtUploadCancel:
		p.handleUploadCancel(ctx, data)
	default:
		p.server.logger.Warn("unknown extension message type", "session_id", p.session.ID, "type", msgType)
	}
}

func chooseSecurityType(available []uint8, password string) uint8 {
	if password != "" {
		for _, t := range available {
			if t == rfb.SecurityVNCAuth {
				return rfb.SecurityVNCAuth
			}
		}
	}
	for _, t := range available {
		if t == rfb.SecurityNone {
			return rfb.SecurityNone
		}
	}
	return available[0]
}

// bufReadWriter adapts a bufio.Reader and bufio.Writer to an io.ReadWriter.
type bufReadWriter struct {
	br *bufio.Reader
	bw *bufio.Writer
}

func (rw *bufReadWriter) Read(p []byte) (int, error) {
	return rw.br.Read(p)
}

func (rw *bufReadWriter) Write(p []byte) (int, error) {
	return rw.bw.Write(p)
}
