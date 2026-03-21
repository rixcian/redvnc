package encodings

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"

	"github.com/rixcian/redvnc/rfb"
)

const zrleTileSize = 64

// ZRLE encodes pixel data using ZRLE encoding (type 16).
// The zlib stream is persistent across calls for dictionary sharing.
type ZRLE struct {
	buf    bytes.Buffer
	writer *zlib.Writer
}

func (z *ZRLE) Type() int32 { return rfb.EncodingZRLE }

func (z *ZRLE) Encode(x, y, width, height uint16, pixels []byte, stride int) (*rfb.Rectangle, error) {
	if z.writer == nil {
		z.writer = zlib.NewWriter(&z.buf)
	}
	z.buf.Reset()

	bpp := 4 // bytes per pixel (32-bit BGRA)

	// Process 64x64 tiles
	for tileY := int(y); tileY < int(y)+int(height); tileY += zrleTileSize {
		tileH := min(zrleTileSize, int(y)+int(height)-tileY)
		for tileX := int(x); tileX < int(x)+int(width); tileX += zrleTileSize {
			tileW := min(zrleTileSize, int(x)+int(width)-tileX)

			if err := z.encodeTile(tileX, tileY, tileW, tileH, pixels, stride, bpp); err != nil {
				return nil, fmt.Errorf("zrle encode tile: %w", err)
			}
		}
	}

	if err := z.writer.Flush(); err != nil {
		return nil, fmt.Errorf("zrle zlib flush: %w", err)
	}

	// ZRLE: 4-byte length prefix + compressed data
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
			Encoding: rfb.EncodingZRLE,
		},
		Data: data,
	}, nil
}

// encodeTile encodes a single tile into the zlib stream.
func (z *ZRLE) encodeTile(tileX, tileY, tileW, tileH int, pixels []byte, stride, bpp int) error {
	// Extract tile pixels as CPIXEL (3 bytes: B, G, R — dropping alpha)
	tilePixels := make([]byte, tileW*tileH*3)
	for row := 0; row < tileH; row++ {
		srcY := tileY + row
		for col := 0; col < tileW; col++ {
			srcX := tileX + col
			srcOff := srcY*stride + srcX*bpp
			if srcOff+3 >= len(pixels) {
				return fmt.Errorf("pixel data out of bounds")
			}
			dstOff := (row*tileW + col) * 3
			// CPIXEL in ZRLE for 32-bit true color: 3 bytes in little-endian (B, G, R)
			tilePixels[dstOff] = pixels[srcOff]     // B
			tilePixels[dstOff+1] = pixels[srcOff+1] // G
			tilePixels[dstOff+2] = pixels[srcOff+2] // R
		}
	}

	numPixels := tileW * tileH

	// Count unique colors (up to 128 for palette modes)
	colorSet := make(map[[3]byte]uint8)
	for i := 0; i < numPixels; i++ {
		var c [3]byte
		copy(c[:], tilePixels[i*3:i*3+3])
		if _, ok := colorSet[c]; !ok {
			if len(colorSet) >= 128 {
				break
			}
			colorSet[c] = uint8(len(colorSet))
		}
	}

	uniqueColors := len(colorSet)

	switch {
	case uniqueColors == 1:
		return z.encodeSolid(tilePixels)
	case uniqueColors >= 2 && uniqueColors <= 16:
		return z.encodePackedPalette(tilePixels, tileW, tileH, colorSet)
	default:
		return z.encodeRaw(tilePixels, numPixels)
	}
}

// encodeSolid encodes a single-color tile (subtype 1).
func (z *ZRLE) encodeSolid(tilePixels []byte) error {
	// Subencoding type 1 = solid
	if _, err := z.writer.Write([]byte{1}); err != nil {
		return err
	}
	// Write the single CPIXEL
	_, err := z.writer.Write(tilePixels[0:3])
	return err
}

// encodeRaw encodes a tile as raw CPIXEL data (subtype 0).
func (z *ZRLE) encodeRaw(tilePixels []byte, numPixels int) error {
	// Subencoding type 0 = raw
	if _, err := z.writer.Write([]byte{0}); err != nil {
		return err
	}
	_, err := z.writer.Write(tilePixels[:numPixels*3])
	return err
}

// encodePackedPalette encodes a tile with a palette and packed indices (subtypes 2-16).
func (z *ZRLE) encodePackedPalette(tilePixels []byte, tileW, tileH int, colorSet map[[3]byte]uint8) error {
	paletteSize := len(colorSet)

	// Subencoding type = palette size (2-16)
	if _, err := z.writer.Write([]byte{uint8(paletteSize)}); err != nil {
		return err
	}

	// Build ordered palette and write it
	palette := make([][3]byte, paletteSize)
	for c, idx := range colorSet {
		palette[idx] = c
	}
	for _, c := range palette {
		if _, err := z.writer.Write(c[:]); err != nil {
			return err
		}
	}

	numPixels := tileW * tileH

	// Determine bits per index
	var bitsPerIndex int
	switch {
	case paletteSize == 2:
		bitsPerIndex = 1
	case paletteSize <= 4:
		bitsPerIndex = 2
	default:
		bitsPerIndex = 4
	}

	// Pack indices into bytes, row by row with padding to byte boundary per row
	for row := 0; row < tileH; row++ {
		var currentByte byte
		bitsUsed := 0

		for col := 0; col < tileW; col++ {
			pixIdx := row*tileW + col
			if pixIdx >= numPixels {
				break
			}
			var c [3]byte
			copy(c[:], tilePixels[pixIdx*3:pixIdx*3+3])
			idx := colorSet[c]

			currentByte = (currentByte << uint(bitsPerIndex)) | (idx & ((1 << uint(bitsPerIndex)) - 1))
			bitsUsed += bitsPerIndex

			if bitsUsed == 8 {
				if _, err := z.writer.Write([]byte{currentByte}); err != nil {
					return err
				}
				currentByte = 0
				bitsUsed = 0
			}
		}

		// Pad remaining bits in row to byte boundary
		if bitsUsed > 0 {
			currentByte <<= uint(8 - bitsUsed)
			if _, err := z.writer.Write([]byte{currentByte}); err != nil {
				return err
			}
		}
	}

	return nil
}

// Reset releases the zlib writer resources.
func (z *ZRLE) Reset() {
	if z.writer != nil {
		z.writer.Close()
		z.writer = nil
	}
	z.buf.Reset()
}
