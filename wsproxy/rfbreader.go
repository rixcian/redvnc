package wsproxy

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

// rfbReader reads complete RFB server-to-client messages from a buffered TCP
// stream. It understands RFB message framing so that each call to ReadMessage
// returns exactly one complete message aligned on message boundaries.
type rfbReader struct {
	br *bufio.Reader

	mu          sync.Mutex
	bpp         int // bytes per pixel (e.g. 4 for 32bpp)
	tightCPixel int // CPIXEL size for Tight encoding (3 for 32bpp/24depth/truecolour)

	// Reusable scratch buffers to avoid per-tile allocations.
	// At 510 tiles per 1920x1080 FBU, this eliminates ~510 allocs/frame.
	rectHdr  [12]byte
	copyBuf  [64 * 64 * 4]byte // max tile size: 64x64 * 4bpp
}

func newRFBReader(br *bufio.Reader, bitsPerPixel, depth, trueColour uint8) *rfbReader {
	bytesPerPixel := int(bitsPerPixel) / 8
	if bytesPerPixel < 1 {
		bytesPerPixel = 4
	}
	cpixel := bytesPerPixel
	if bitsPerPixel == 32 && depth == 24 && trueColour != 0 {
		cpixel = 3
	}
	return &rfbReader{
		br:          br,
		bpp:         bytesPerPixel,
		tightCPixel: cpixel,
	}
}

// UpdatePixelFormat updates the pixel format used for encoding data length
// calculations. Called when the client sends SetPixelFormat.
func (r *rfbReader) UpdatePixelFormat(bitsPerPixel, depth, trueColour uint8) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bpp = int(bitsPerPixel) / 8
	if r.bpp < 1 {
		r.bpp = 4
	}
	r.tightCPixel = r.bpp
	if bitsPerPixel == 32 && depth == 24 && trueColour != 0 {
		r.tightCPixel = 3
	}
}

// ReadMessage reads a complete RFB server-to-client message and returns its
// raw bytes. The returned slice contains the full message including the type
// byte and is suitable for forwarding to a WebSocket client as-is.
func (r *rfbReader) ReadMessage() ([]byte, error) {
	r.mu.Lock()
	bpp := r.bpp
	cpixel := r.tightCPixel
	r.mu.Unlock()

	msgType, err := r.br.ReadByte()
	if err != nil {
		return nil, err
	}

	switch msgType {
	case 0: // FramebufferUpdate
		return r.readFramebufferUpdate(bpp, cpixel)
	case 1: // SetColourMapEntries
		return r.readSetColourMapEntries()
	case 2: // Bell
		return []byte{2}, nil
	case 3: // ServerCutText
		return r.readServerCutText()
	default:
		return nil, fmt.Errorf("unknown RFB server message type: %d", msgType)
	}
}

func (r *rfbReader) readFramebufferUpdate(bpp, cpixel int) ([]byte, error) {
	// Already consumed type byte (0).
	// Read: padding(1) + numRects(2) = 3 bytes
	hdr := make([]byte, 3)
	if _, err := io.ReadFull(r.br, hdr); err != nil {
		return nil, fmt.Errorf("fbu header: %w", err)
	}
	numRects := int(binary.BigEndian.Uint16(hdr[1:3]))

	var buf bytes.Buffer
	buf.Grow(4 + numRects*12) // at minimum
	buf.WriteByte(0)          // type
	buf.Write(hdr)

	for i := 0; i < numRects; i++ {
		// rect header: x(2)+y(2)+w(2)+h(2)+encoding(4) = 12 bytes
		if _, err := io.ReadFull(r.br, r.rectHdr[:]); err != nil {
			return nil, fmt.Errorf("rect header %d/%d: %w", i, numRects, err)
		}
		buf.Write(r.rectHdr[:])

		w := int(binary.BigEndian.Uint16(r.rectHdr[4:6]))
		h := int(binary.BigEndian.Uint16(r.rectHdr[6:8]))
		enc := int32(binary.BigEndian.Uint32(r.rectHdr[8:12]))

		if err := r.readEncodingData(&buf, w, h, enc, bpp, cpixel); err != nil {
			return nil, fmt.Errorf("encoding data rect %d/%d enc=%d: %w", i, numRects, enc, err)
		}
	}

	return buf.Bytes(), nil
}

func (r *rfbReader) readEncodingData(buf *bytes.Buffer, w, h int, enc int32, bpp, cpixel int) error {
	switch enc {
	case 0: // Raw
		return r.readExact(buf, w*h*bpp)
	case 1: // CopyRect
		return r.readExact(buf, 4)
	case 2: // RRE
		return r.readRRE(buf, bpp)
	case 6: // Zlib
		return r.readLengthPrefixed32(buf)
	case 7: // Tight
		return r.readTightData(buf, w, h, cpixel)
	case 16: // ZRLE
		return r.readLengthPrefixed32(buf)
	case -239: // Cursor
		pixelLen := w * h * bpp
		maskLen := ((w + 7) / 8) * h
		return r.readExact(buf, pixelLen+maskLen)
	case -223: // DesktopSize
		return nil // no encoding data
	default:
		return fmt.Errorf("unsupported encoding %d", enc)
	}
}

func (r *rfbReader) readRRE(buf *bytes.Buffer, bpp int) error {
	// numSubRects(4) + bgPixel(bpp) + subrects(numSubRects * (bpp + 8))
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(r.br, hdr); err != nil {
		return err
	}
	buf.Write(hdr)
	numSubRects := int(binary.BigEndian.Uint32(hdr))
	return r.readExact(buf, bpp+numSubRects*(bpp+8))
}

func (r *rfbReader) readTightData(buf *bytes.Buffer, w, h, cpixel int) error {
	for ty := 0; ty < h; ty += 64 {
		tileH := 64
		if ty+tileH > h {
			tileH = h - ty
		}
		for tx := 0; tx < w; tx += 64 {
			tileW := 64
			if tx+tileW > w {
				tileW = w - tx
			}

			control, err := r.br.ReadByte()
			if err != nil {
				return err
			}
			buf.WriteByte(control)

			subType := control & 0x0f

			switch {
			case subType == 0x08:
				// Fill: single CPIXEL value
				if err := r.readExact(buf, cpixel); err != nil {
					return err
				}

			case subType == 0x09:
				// JPEG: compact-length + JPEG data
				if err := r.readCompactPrefixed(buf); err != nil {
					return err
				}

			case subType <= 0x07:
				// Basic compression
				if err := r.readTightBasic(buf, tileW, tileH, cpixel, subType); err != nil {
					return err
				}

			default:
				return fmt.Errorf("unsupported tight subtype 0x%02x", subType)
			}
		}
	}
	return nil
}

func (r *rfbReader) readTightBasic(buf *bytes.Buffer, tileW, tileH, cpixel int, subType byte) error {
	readFilter := (subType & 0x04) != 0
	filterID := byte(0) // copy filter (no filter)

	if readFilter {
		var err error
		filterID, err = r.br.ReadByte()
		if err != nil {
			return err
		}
		buf.WriteByte(filterID)
	}

	var rawDataSize int

	switch filterID {
	case 0: // Copy filter (raw pixels)
		rawDataSize = tileW * tileH * cpixel

	case 1: // Palette filter
		ps, err := r.br.ReadByte()
		if err != nil {
			return err
		}
		buf.WriteByte(ps)
		paletteSize := int(ps) + 1

		// Read palette entries
		if err := r.readExact(buf, paletteSize*cpixel); err != nil {
			return err
		}

		// Packed pixel indices
		if paletteSize == 2 {
			// 1 bit per pixel, padded to byte per row
			rawDataSize = ((tileW + 7) / 8) * tileH
		} else {
			// 1 byte per pixel
			rawDataSize = tileW * tileH
		}

	case 2: // Gradient filter
		rawDataSize = tileW * tileH * cpixel

	default:
		return fmt.Errorf("unknown tight filter: %d", filterID)
	}

	if rawDataSize < 12 {
		// Data sent uncompressed (no length prefix)
		return r.readExact(buf, rawDataSize)
	}
	// Compressed with compact-length prefix
	return r.readCompactPrefixed(buf)
}

func (r *rfbReader) readExact(buf *bytes.Buffer, n int) error {
	if n <= 0 {
		return nil
	}
	// Use the scratch buffer for reads that fit (avoids allocation).
	// Most Tight tile reads are small enough (max ~12KB decompressed,
	// but compressed data + control bytes are much smaller).
	if n <= len(r.copyBuf) {
		s := r.copyBuf[:n]
		if _, err := io.ReadFull(r.br, s); err != nil {
			return err
		}
		buf.Write(s)
		return nil
	}
	// Large reads: allocate
	data := make([]byte, n)
	if _, err := io.ReadFull(r.br, data); err != nil {
		return err
	}
	buf.Write(data)
	return nil
}

func (r *rfbReader) readLengthPrefixed32(buf *bytes.Buffer) error {
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(r.br, lenBuf); err != nil {
		return err
	}
	buf.Write(lenBuf)
	dataLen := int(binary.BigEndian.Uint32(lenBuf))
	return r.readExact(buf, dataLen)
}

func (r *rfbReader) readCompactPrefixed(buf *bytes.Buffer) error {
	length, err := r.readCompactLength(buf)
	if err != nil {
		return err
	}
	return r.readExact(buf, length)
}

func (r *rfbReader) readCompactLength(buf *bytes.Buffer) (int, error) {
	b1, err := r.br.ReadByte()
	if err != nil {
		return 0, err
	}
	buf.WriteByte(b1)
	length := int(b1) & 0x7f

	if b1&0x80 != 0 {
		b2, err := r.br.ReadByte()
		if err != nil {
			return 0, err
		}
		buf.WriteByte(b2)
		length |= (int(b2) & 0x7f) << 7

		if b2&0x80 != 0 {
			b3, err := r.br.ReadByte()
			if err != nil {
				return 0, err
			}
			buf.WriteByte(b3)
			length |= int(b3) << 14
		}
	}

	return length, nil
}

func (r *rfbReader) readServerCutText() ([]byte, error) {
	// Already consumed type byte (3).
	// Read: padding(3) + length(4) = 7 bytes
	hdr := make([]byte, 7)
	if _, err := io.ReadFull(r.br, hdr); err != nil {
		return nil, err
	}
	textLen := int(binary.BigEndian.Uint32(hdr[3:7]))

	msg := make([]byte, 8+textLen)
	msg[0] = 3
	copy(msg[1:8], hdr)
	if textLen > 0 {
		if _, err := io.ReadFull(r.br, msg[8:]); err != nil {
			return nil, err
		}
	}
	return msg, nil
}

func (r *rfbReader) readSetColourMapEntries() ([]byte, error) {
	// Already consumed type byte (1).
	// Read: padding(1) + firstColour(2) + numColours(2) = 5 bytes
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(r.br, hdr); err != nil {
		return nil, err
	}
	numColours := int(binary.BigEndian.Uint16(hdr[3:5]))
	dataLen := numColours * 6

	msg := make([]byte, 6+dataLen)
	msg[0] = 1
	copy(msg[1:6], hdr)
	if dataLen > 0 {
		if _, err := io.ReadFull(r.br, msg[6:]); err != nil {
			return nil, err
		}
	}
	return msg, nil
}
