package rfb

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/des"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"image/jpeg"
	"io"
	"net"
	"sync"
)

// ClientConfig holds configuration for a VNC client connection.
type ClientConfig struct {
	// Password for VNC authentication. Leave empty for no-auth.
	Password string

	// Shared indicates whether the server should allow other clients to stay connected.
	Shared bool

	// Encodings is the list of supported encodings, in order of preference.
	// If empty, only Raw encoding is requested.
	Encodings []int32

	// TLSConfig enables TLS encryption on the client. If non-nil, the client
	// wraps the connection in TLS before the RFB handshake.
	TLSConfig *tls.Config
}

// Client represents a VNC client connected to a server.
type Client struct {
	conn   net.Conn
	br     *bufio.Reader
	bw     *bufio.Writer
	config ClientConfig

	// Server properties learned during handshake.
	Width       uint16
	Height      uint16
	PixelFormat PixelFormat
	Name        string

	mu sync.Mutex

	tightStreams [4]*bytes.Buffer // persistent zlib decompression buffers for Tight
}

// Connect establishes a VNC client connection to the given address.
func Connect(addr string, config ClientConfig) (*Client, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	if config.TLSConfig != nil {
		conn = tls.Client(conn, config.TLSConfig)
	}

	c := &Client{
		conn:   conn,
		br:     bufio.NewReader(conn),
		bw:     bufio.NewWriter(conn),
		config: config,
	}

	if err := c.handshake(); err != nil {
		conn.Close()
		return nil, err
	}

	return c, nil
}

func (c *Client) handshake() error {
	// Read server version
	serverVersion := make([]byte, 12)
	if _, err := io.ReadFull(c.br, serverVersion); err != nil {
		return fmt.Errorf("read server version: %w", err)
	}

	// Send our version (3.8)
	if _, err := c.bw.WriteString(VersionString3_8); err != nil {
		return fmt.Errorf("write version: %w", err)
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}

	// Read security types
	var numTypes uint8
	if err := binary.Read(c.br, binary.BigEndian, &numTypes); err != nil {
		return fmt.Errorf("read security type count: %w", err)
	}
	if numTypes == 0 {
		var reasonLen uint32
		if err := binary.Read(c.br, binary.BigEndian, &reasonLen); err != nil {
			return fmt.Errorf("read failure reason length: %w", err)
		}
		reason := make([]byte, reasonLen)
		if _, err := io.ReadFull(c.br, reason); err != nil {
			return fmt.Errorf("read failure reason: %w", err)
		}
		return fmt.Errorf("server refused connection: %s", string(reason))
	}

	secTypes := make([]uint8, numTypes)
	for i := range secTypes {
		if err := binary.Read(c.br, binary.BigEndian, &secTypes[i]); err != nil {
			return fmt.Errorf("read security type: %w", err)
		}
	}

	// Choose security type
	chosenType := c.chooseSecurityType(secTypes)
	if err := binary.Write(c.bw, binary.BigEndian, chosenType); err != nil {
		return fmt.Errorf("write security choice: %w", err)
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}

	// Perform security handshake
	switch chosenType {
	case SecurityNone:
		// Nothing to do
	case SecurityVNCAuth:
		if err := c.performVNCAuth(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported security type: %d", chosenType)
	}

	// Read SecurityResult
	var secResult uint32
	if err := binary.Read(c.br, binary.BigEndian, &secResult); err != nil {
		return fmt.Errorf("read security result: %w", err)
	}
	if secResult != 0 {
		var reasonLen uint32
		if err := binary.Read(c.br, binary.BigEndian, &reasonLen); err != nil {
			return fmt.Errorf("authentication failed (could not read reason)")
		}
		reason := make([]byte, reasonLen)
		if _, err := io.ReadFull(c.br, reason); err != nil {
			return fmt.Errorf("authentication failed (could not read reason text)")
		}
		return fmt.Errorf("authentication failed: %s", string(reason))
	}

	// Send ClientInit
	sharedFlag := uint8(0)
	if c.config.Shared {
		sharedFlag = 1
	}
	if err := binary.Write(c.bw, binary.BigEndian, sharedFlag); err != nil {
		return fmt.Errorf("write client init: %w", err)
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}

	// Read ServerInit
	if err := binary.Read(c.br, binary.BigEndian, &c.Width); err != nil {
		return fmt.Errorf("read width: %w", err)
	}
	if err := binary.Read(c.br, binary.BigEndian, &c.Height); err != nil {
		return fmt.Errorf("read height: %w", err)
	}
	if err := binary.Read(c.br, binary.BigEndian, &c.PixelFormat); err != nil {
		return fmt.Errorf("read pixel format: %w", err)
	}
	var nameLen uint32
	if err := binary.Read(c.br, binary.BigEndian, &nameLen); err != nil {
		return fmt.Errorf("read name length: %w", err)
	}
	if nameLen > 1024 {
		return fmt.Errorf("server name too long: %d", nameLen)
	}
	nameBytes := make([]byte, nameLen)
	if _, err := io.ReadFull(c.br, nameBytes); err != nil {
		return fmt.Errorf("read name: %w", err)
	}
	c.Name = string(nameBytes)

	// Send SetEncodings if configured
	encodings := c.config.Encodings
	if len(encodings) == 0 {
		encodings = []int32{EncodingRaw}
	}
	return c.sendSetEncodings(encodings)
}

func (c *Client) chooseSecurityType(available []uint8) uint8 {
	if c.config.Password != "" {
		for _, t := range available {
			if t == SecurityVNCAuth {
				return SecurityVNCAuth
			}
		}
	}
	for _, t := range available {
		if t == SecurityNone {
			return SecurityNone
		}
	}
	return available[0]
}

func (c *Client) performVNCAuth() error {
	// Read 16-byte challenge
	challenge := make([]byte, 16)
	if _, err := io.ReadFull(c.br, challenge); err != nil {
		return fmt.Errorf("read vnc auth challenge: %w", err)
	}

	// Prepare DES key from password (VNC-style bit reversal)
	key := make([]byte, 8)
	copy(key, []byte(c.config.Password))
	for i := range key {
		key[i] = clientReverseBits(key[i])
	}

	cipher, err := des.NewCipher(key)
	if err != nil {
		return fmt.Errorf("create des cipher: %w", err)
	}

	response := make([]byte, 16)
	cipher.Encrypt(response[:8], challenge[:8])
	cipher.Encrypt(response[8:], challenge[8:])

	if _, err := c.bw.Write(response); err != nil {
		return fmt.Errorf("write vnc auth response: %w", err)
	}
	return c.bw.Flush()
}

func clientReverseBits(b byte) byte {
	var result byte
	for i := 0; i < 8; i++ {
		result = (result << 1) | (b & 1)
		b >>= 1
	}
	return result
}

func (c *Client) sendSetEncodings(encodings []int32) error {
	if err := binary.Write(c.bw, binary.BigEndian, uint8(MsgSetEncodings)); err != nil {
		return err
	}
	if err := binary.Write(c.bw, binary.BigEndian, uint8(0)); err != nil { // padding
		return err
	}
	if err := binary.Write(c.bw, binary.BigEndian, uint16(len(encodings))); err != nil {
		return err
	}
	for _, enc := range encodings {
		if err := binary.Write(c.bw, binary.BigEndian, enc); err != nil {
			return err
		}
	}
	return c.bw.Flush()
}

// RequestFramebufferUpdate sends a FramebufferUpdateRequest to the server.
func (c *Client) RequestFramebufferUpdate(incremental bool, x, y, width, height uint16) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	inc := uint8(0)
	if incremental {
		inc = 1
	}

	if err := binary.Write(c.bw, binary.BigEndian, uint8(MsgFramebufferUpdateRequest)); err != nil {
		return err
	}
	if err := binary.Write(c.bw, binary.BigEndian, inc); err != nil {
		return err
	}
	if err := binary.Write(c.bw, binary.BigEndian, x); err != nil {
		return err
	}
	if err := binary.Write(c.bw, binary.BigEndian, y); err != nil {
		return err
	}
	if err := binary.Write(c.bw, binary.BigEndian, width); err != nil {
		return err
	}
	if err := binary.Write(c.bw, binary.BigEndian, height); err != nil {
		return err
	}
	return c.bw.Flush()
}

// SendKeyEvent sends a key event to the server.
func (c *Client) SendKeyEvent(down bool, key uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	downFlag := uint8(0)
	if down {
		downFlag = 1
	}

	if err := binary.Write(c.bw, binary.BigEndian, uint8(MsgKeyEvent)); err != nil {
		return err
	}
	if err := binary.Write(c.bw, binary.BigEndian, downFlag); err != nil {
		return err
	}
	if _, err := c.bw.Write([]byte{0, 0}); err != nil { // padding
		return err
	}
	if err := binary.Write(c.bw, binary.BigEndian, key); err != nil {
		return err
	}
	return c.bw.Flush()
}

// SendPointerEvent sends a pointer event to the server.
func (c *Client) SendPointerEvent(buttonMask uint8, x, y uint16) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := binary.Write(c.bw, binary.BigEndian, uint8(MsgPointerEvent)); err != nil {
		return err
	}
	if err := binary.Write(c.bw, binary.BigEndian, buttonMask); err != nil {
		return err
	}
	if err := binary.Write(c.bw, binary.BigEndian, x); err != nil {
		return err
	}
	if err := binary.Write(c.bw, binary.BigEndian, y); err != nil {
		return err
	}
	return c.bw.Flush()
}

// ReadMessage reads the next server-to-client message.
func (c *Client) ReadMessage() (uint8, interface{}, error) {
	var msgType uint8
	if err := binary.Read(c.br, binary.BigEndian, &msgType); err != nil {
		return 0, nil, fmt.Errorf("read message type: %w", err)
	}

	switch msgType {
	case MsgFramebufferUpdate:
		update, err := c.readFramebufferUpdate()
		return msgType, update, err
	case MsgBell:
		return msgType, nil, nil
	case MsgServerCutText:
		text, err := c.readServerCutText()
		return msgType, text, err
	case MsgSetColourMapEntry:
		return msgType, nil, fmt.Errorf("SetColourMapEntry not implemented")
	default:
		return msgType, nil, fmt.Errorf("unknown server message type: %d", msgType)
	}
}

// FramebufferUpdate holds the rectangles from a framebuffer update message.
type FramebufferUpdate struct {
	Rects []ReceivedRect
}

// ReceivedRect is a rectangle received in a framebuffer update.
type ReceivedRect struct {
	X, Y, Width, Height uint16
	Encoding            int32
	Data                []byte
}

func (c *Client) readFramebufferUpdate() (*FramebufferUpdate, error) {
	var padding uint8
	if err := binary.Read(c.br, binary.BigEndian, &padding); err != nil {
		return nil, err
	}
	var numRects uint16
	if err := binary.Read(c.br, binary.BigEndian, &numRects); err != nil {
		return nil, err
	}

	update := &FramebufferUpdate{
		Rects: make([]ReceivedRect, numRects),
	}

	for i := range update.Rects {
		rect := &update.Rects[i]
		if err := binary.Read(c.br, binary.BigEndian, &rect.X); err != nil {
			return nil, err
		}
		if err := binary.Read(c.br, binary.BigEndian, &rect.Y); err != nil {
			return nil, err
		}
		if err := binary.Read(c.br, binary.BigEndian, &rect.Width); err != nil {
			return nil, err
		}
		if err := binary.Read(c.br, binary.BigEndian, &rect.Height); err != nil {
			return nil, err
		}
		if err := binary.Read(c.br, binary.BigEndian, &rect.Encoding); err != nil {
			return nil, err
		}

		switch rect.Encoding {
		case EncodingRaw:
			dataLen := int(rect.Width) * int(rect.Height) * 4
			rect.Data = make([]byte, dataLen)
			if _, err := io.ReadFull(c.br, rect.Data); err != nil {
				return nil, fmt.Errorf("read raw rect data: %w", err)
			}
		case EncodingCopyRect:
			rect.Data = make([]byte, 4)
			if _, err := io.ReadFull(c.br, rect.Data); err != nil {
				return nil, fmt.Errorf("read copyrect data: %w", err)
			}
		case EncodingZlib:
			var compressedLen uint32
			if err := binary.Read(c.br, binary.BigEndian, &compressedLen); err != nil {
				return nil, fmt.Errorf("read zlib length: %w", err)
			}
			rect.Data = make([]byte, compressedLen)
			if _, err := io.ReadFull(c.br, rect.Data); err != nil {
				return nil, fmt.Errorf("read zlib data: %w", err)
			}
		case EncodingTight:
			data, err := c.readTightRect(int(rect.Width), int(rect.Height))
			if err != nil {
				return nil, fmt.Errorf("read tight rect: %w", err)
			}
			rect.Data = data
		case EncodingCursor:
			// Cursor pseudo-encoding: pixel data + bitmask.
			// X,Y in the header are the hotspot coordinates.
			bpp := int(c.PixelFormat.BitsPerPixel) / 8
			pixelDataLen := int(rect.Width) * int(rect.Height) * bpp
			maskRowBytes := (int(rect.Width) + 7) / 8
			maskLen := maskRowBytes * int(rect.Height)
			rect.Data = make([]byte, pixelDataLen+maskLen)
			if _, err := io.ReadFull(c.br, rect.Data); err != nil {
				return nil, fmt.Errorf("read cursor data: %w", err)
			}
		case EncodingDesktopSize:
			// DesktopSize pseudo-encoding: no pixel data.
			// Width/Height in the header carry the new screen dimensions.
			c.Width = rect.Width
			c.Height = rect.Height
			rect.Data = nil
		default:
			return nil, fmt.Errorf("unsupported encoding: %d", rect.Encoding)
		}
	}

	return update, nil
}

func (c *Client) readServerCutText() (string, error) {
	var padding [3]byte
	if _, err := io.ReadFull(c.br, padding[:]); err != nil {
		return "", err
	}
	var length uint32
	if err := binary.Read(c.br, binary.BigEndian, &length); err != nil {
		return "", err
	}
	if length > 10*1024*1024 {
		return "", fmt.Errorf("server cut text too large: %d", length)
	}
	text := make([]byte, length)
	if _, err := io.ReadFull(c.br, text); err != nil {
		return "", err
	}
	return string(text), nil
}

// readTightRect reads a Tight-encoded rectangle and returns decompressed BGRA pixel data
// in scanline order (compatible with raw encoding layout).
func (c *Client) readTightRect(width, height int) ([]byte, error) {
	const tileSize = 64

	result := make([]byte, width*height*4)

	for tileY := 0; tileY < height; tileY += tileSize {
		tileH := tileSize
		if tileY+tileH > height {
			tileH = height - tileY
		}
		for tileX := 0; tileX < width; tileX += tileSize {
			tileW := tileSize
			if tileX+tileW > width {
				tileW = width - tileX
			}

			tileData, err := c.readTightTile(tileW, tileH)
			if err != nil {
				return nil, fmt.Errorf("tight tile (%d,%d): %w", tileX, tileY, err)
			}

			// Copy tile pixels into the correct scanline positions
			for row := 0; row < tileH; row++ {
				dstOff := ((tileY+row)*width + tileX) * 4
				srcOff := row * tileW * 4
				copy(result[dstOff:dstOff+tileW*4], tileData[srcOff:srcOff+tileW*4])
			}
		}
	}

	return result, nil
}

func (c *Client) readTightTile(tileW, tileH int) ([]byte, error) {
	// Read compression control byte
	var controlByte uint8
	if err := binary.Read(c.br, binary.BigEndian, &controlByte); err != nil {
		return nil, fmt.Errorf("read tight control byte: %w", err)
	}

	subEncoding := controlByte & 0x0F

	switch {
	case subEncoding == 0x08: // Fill
		rgb := make([]byte, 3)
		if _, err := io.ReadFull(c.br, rgb); err != nil {
			return nil, fmt.Errorf("read tight fill color: %w", err)
		}
		// Convert RGB to BGRA tiles
		pixels := make([]byte, tileW*tileH*4)
		for i := 0; i < tileW*tileH; i++ {
			off := i * 4
			pixels[off] = rgb[2]   // B
			pixels[off+1] = rgb[1] // G
			pixels[off+2] = rgb[0] // R
			pixels[off+3] = 255    // A
		}
		return pixels, nil

	case subEncoding == 0x09: // JPEG
		length, err := c.readCompactLen()
		if err != nil {
			return nil, fmt.Errorf("read tight jpeg length: %w", err)
		}
		jpegData := make([]byte, length)
		if _, err := io.ReadFull(c.br, jpegData); err != nil {
			return nil, fmt.Errorf("read tight jpeg data: %w", err)
		}
		img, err := jpeg.Decode(bytes.NewReader(jpegData))
		if err != nil {
			return nil, fmt.Errorf("decode tight jpeg: %w", err)
		}
		bounds := img.Bounds()
		pixels := make([]byte, bounds.Dx()*bounds.Dy()*4)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				off := ((y-bounds.Min.Y)*bounds.Dx() + (x - bounds.Min.X)) * 4
				pixels[off] = byte(b >> 8)   // B
				pixels[off+1] = byte(g >> 8) // G
				pixels[off+2] = byte(r >> 8) // R
				pixels[off+3] = 255          // A
			}
		}
		return pixels, nil

	default: // Basic (0x00-0x07)
		length, err := c.readCompactLen()
		if err != nil {
			return nil, fmt.Errorf("read tight basic length: %w", err)
		}
		compressed := make([]byte, length)
		if _, err := io.ReadFull(c.br, compressed); err != nil {
			return nil, fmt.Errorf("read tight basic data: %w", err)
		}
		reader, err := zlib.NewReader(bytes.NewReader(compressed))
		if err != nil {
			return nil, fmt.Errorf("zlib reader: %w", err)
		}
		rgb, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			return nil, fmt.Errorf("decompress tight basic: %w", err)
		}
		// Convert RGB to BGRA
		numPixels := len(rgb) / 3
		pixels := make([]byte, numPixels*4)
		for i := 0; i < numPixels; i++ {
			srcOff := i * 3
			dstOff := i * 4
			pixels[dstOff] = rgb[srcOff+2]   // B
			pixels[dstOff+1] = rgb[srcOff+1] // G
			pixels[dstOff+2] = rgb[srcOff]   // R
			pixels[dstOff+3] = 255           // A
		}
		return pixels, nil
	}
}

func (c *Client) readCompactLen() (int, error) {
	var b [1]byte
	if _, err := io.ReadFull(c.br, b[:]); err != nil {
		return 0, err
	}
	n := int(b[0]) & 0x7F
	if b[0]&0x80 == 0 {
		return n, nil
	}
	if _, err := io.ReadFull(c.br, b[:]); err != nil {
		return 0, err
	}
	n |= (int(b[0]) & 0x7F) << 7
	if b[0]&0x80 == 0 {
		return n, nil
	}
	if _, err := io.ReadFull(c.br, b[:]); err != nil {
		return 0, err
	}
	n |= int(b[0]) << 14
	return n, nil
}

// Close closes the client connection.
func (c *Client) Close() error {
	return c.conn.Close()
}
