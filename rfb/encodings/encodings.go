// Package encodings implements RFB framebuffer encoding types.
package encodings

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"

	"github.com/redamp-io/redvnc/rfb"
)

// Encoder encodes framebuffer pixel data into RFB rectangles.
type Encoder interface {
	// Encode encodes pixel data for the given region into an RFB rectangle.
	// pixels is raw BGRA pixel data, stride is the number of bytes per row of the full framebuffer.
	Encode(x, y, width, height uint16, pixels []byte, stride int) (*rfb.Rectangle, error)
	// Type returns the encoding type constant.
	Type() int32
}

// Raw encodes pixel data with no compression (encoding type 0).
type Raw struct{}

func (r *Raw) Type() int32 { return rfb.EncodingRaw }

func (r *Raw) Encode(x, y, width, height uint16, pixels []byte, stride int) (*rfb.Rectangle, error) {
	bpp := 4 // bytes per pixel (32-bit)
	rectData := make([]byte, 0, int(width)*int(height)*bpp)

	for row := 0; row < int(height); row++ {
		srcY := int(y) + row
		srcOffset := srcY*stride + int(x)*bpp
		srcEnd := srcOffset + int(width)*bpp
		if srcEnd > len(pixels) {
			return nil, fmt.Errorf("raw encode: pixel data out of bounds at row %d", row)
		}
		rectData = append(rectData, pixels[srcOffset:srcEnd]...)
	}

	return &rfb.Rectangle{
		Header: rfb.RectHeader{
			X:        x,
			Y:        y,
			Width:    width,
			Height:   height,
			Encoding: rfb.EncodingRaw,
		},
		Data: rectData,
	}, nil
}

// CopyRect tells the client to copy pixels from another location (encoding type 1).
type CopyRect struct{}

func (c *CopyRect) Type() int32 { return rfb.EncodingCopyRect }

// EncodeCopyRect creates a CopyRect rectangle pointing to a source position.
func (c *CopyRect) EncodeCopyRect(x, y, width, height, srcX, srcY uint16) *rfb.Rectangle {
	data := make([]byte, 4)
	binary.BigEndian.PutUint16(data[0:2], srcX)
	binary.BigEndian.PutUint16(data[2:4], srcY)

	return &rfb.Rectangle{
		Header: rfb.RectHeader{
			X:        x,
			Y:        y,
			Width:    width,
			Height:   height,
			Encoding: rfb.EncodingCopyRect,
		},
		Data: data,
	}
}

// Encode is not used for CopyRect; use EncodeCopyRect instead.
func (c *CopyRect) Encode(x, y, width, height uint16, pixels []byte, stride int) (*rfb.Rectangle, error) {
	return nil, fmt.Errorf("copyrect: use EncodeCopyRect instead")
}

// Zlib encodes pixel data with zlib compression (encoding type 6).
type Zlib struct {
	buf    bytes.Buffer
	writer *zlib.Writer
}

func (z *Zlib) Type() int32 { return rfb.EncodingZlib }

func (z *Zlib) Encode(x, y, width, height uint16, pixels []byte, stride int) (*rfb.Rectangle, error) {
	bpp := 4

	// Initialize zlib writer on first use (reuse across calls for better compression)
	if z.writer == nil {
		z.writer = zlib.NewWriter(&z.buf)
	}

	z.buf.Reset()
	z.writer.Reset(&z.buf)

	for row := 0; row < int(height); row++ {
		srcY := int(y) + row
		srcOffset := srcY*stride + int(x)*bpp
		srcEnd := srcOffset + int(width)*bpp
		if srcEnd > len(pixels) {
			return nil, fmt.Errorf("zlib encode: pixel data out of bounds at row %d", row)
		}
		if _, err := z.writer.Write(pixels[srcOffset:srcEnd]); err != nil {
			return nil, fmt.Errorf("zlib write: %w", err)
		}
	}

	if err := z.writer.Close(); err != nil {
		return nil, fmt.Errorf("zlib close: %w", err)
	}
	z.writer = nil

	// Zlib encoding: 4-byte length prefix + compressed data
	compressedLen := z.buf.Len()
	data := make([]byte, 4+compressedLen)
	binary.BigEndian.PutUint32(data[0:4], uint32(compressedLen))
	copy(data[4:], z.buf.Bytes())

	return &rfb.Rectangle{
		Header: rfb.RectHeader{
			X:        x,
			Y:        y,
			Width:    width,
			Height:   height,
			Encoding: rfb.EncodingZlib,
		},
		Data: data,
	}, nil
}

// Reset releases the zlib writer resources.
func (z *Zlib) Reset() {
	if z.writer != nil {
		z.writer.Close()
		z.writer = nil
	}
	z.buf.Reset()
}
