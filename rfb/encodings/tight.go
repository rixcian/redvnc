package encodings

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"

	"github.com/rixcian/redvnc/rfb"
)

const tileSize = 64

// zlibStream wraps a persistent zlib.Writer with its backing buffer.
type zlibStream struct {
	buf    bytes.Buffer
	writer *zlib.Writer
}

func (s *zlibStream) reset() {
	if s.writer != nil {
		s.writer.Close()
		s.writer = nil
	}
	s.buf.Reset()
}

// Tight encodes pixel data using Tight encoding (encoding type 7).
type Tight struct {
	streams     [4]*zlibStream
	jpegQuality int
}

// NewTight creates a new Tight encoder with the given JPEG quality (0-100).
func NewTight(jpegQuality int) *Tight {
	if jpegQuality <= 0 {
		jpegQuality = 75
	}
	if jpegQuality > 100 {
		jpegQuality = 100
	}
	t := &Tight{jpegQuality: jpegQuality}
	for i := range t.streams {
		t.streams[i] = &zlibStream{}
	}
	return t
}

// Type returns the encoding type constant for Tight.
func (t *Tight) Type() int32 { return rfb.EncodingTight }

// Encode encodes pixel data for the given region into an RFB rectangle using Tight encoding.
// pixels is raw BGRA pixel data, stride is the number of bytes per row of the full framebuffer.
func (t *Tight) Encode(x, y, width, height uint16, pixels []byte, stride int) (*rfb.Rectangle, error) {
	rects, err := t.EncodeMulti(x, y, width, height, pixels, stride)
	if err != nil {
		return nil, err
	}
	// For single-tile rectangles, return directly; otherwise merge (for test compat)
	if len(rects) == 1 {
		return &rects[0], nil
	}
	var buf bytes.Buffer
	for _, r := range rects {
		buf.Write(r.Data)
	}
	return &rfb.Rectangle{
		Header: rfb.RectHeader{
			X:        x,
			Y:        y,
			Width:    width,
			Height:   height,
			Encoding: rfb.EncodingTight,
		},
		Data: buf.Bytes(),
	}, nil
}

// EncodeMulti encodes pixel data into multiple Tight rectangles (one per tile).
// Each rectangle has its own rect header with correct tile position and size.
// pixels is raw BGRA pixel data, stride is the number of bytes per row of the full framebuffer.
func (t *Tight) EncodeMulti(x, y, width, height uint16, pixels []byte, stride int) ([]rfb.Rectangle, error) {
	w := int(width)
	h := int(height)
	var rects []rfb.Rectangle

	for tileY := 0; tileY < h; tileY += tileSize {
		tileH := tileSize
		if tileY+tileH > h {
			tileH = h - tileY
		}
		for tileX := 0; tileX < w; tileX += tileSize {
			tileW := tileSize
			if tileX+tileW > w {
				tileW = w - tileX
			}

			// Extract tile pixels (BGRA) from the framebuffer
			tilePixels := make([]byte, tileW*tileH*4)
			for row := 0; row < tileH; row++ {
				srcY := int(y) + tileY + row
				srcX := int(x) + tileX
				srcOffset := srcY*stride + srcX*4
				srcEnd := srcOffset + tileW*4
				if srcEnd > len(pixels) {
					return nil, fmt.Errorf("tight encode: pixel data out of bounds at tile (%d,%d) row %d", tileX, tileY, row)
				}
				copy(tilePixels[row*tileW*4:], pixels[srcOffset:srcEnd])
			}

			tileData, err := t.encodeTile(tilePixels, tileW, tileH)
			if err != nil {
				return nil, fmt.Errorf("tight encode tile (%d,%d): %w", tileX, tileY, err)
			}

			rects = append(rects, rfb.Rectangle{
				Header: rfb.RectHeader{
					X:        x + uint16(tileX),
					Y:        y + uint16(tileY),
					Width:    uint16(tileW),
					Height:   uint16(tileH),
					Encoding: rfb.EncodingTight,
				},
				Data: tileData,
			})
		}
	}

	return rects, nil
}

// Reset releases all zlib stream resources.
func (t *Tight) Reset() {
	for _, s := range t.streams {
		s.reset()
	}
}

func (t *Tight) encodeTile(tilePixels []byte, tileW, tileH int) ([]byte, error) {
	// Check for solid color
	if r, g, b, ok := t.isSolidColor(tilePixels, tileW, tileH); ok {
		return []byte{0x08, r, g, b}, nil
	}

	// Use JPEG for large, high-variance tiles
	if tileW*tileH >= 4096 && t.colorVariance(tilePixels, tileW, tileH) > 512 {
		return t.encodeJPEG(tilePixels, tileW, tileH)
	}

	// Fall back to Basic (zlib stream 0)
	return t.encodeBasic(tilePixels, tileW, tileH, 0)
}

// isSolidColor checks if all pixels in the tile are the same color.
// Returns RGB values and true if solid.
func (t *Tight) isSolidColor(pixels []byte, w, h int) (r, g, b byte, ok bool) {
	if len(pixels) < 4 {
		return 0, 0, 0, false
	}
	// First pixel: BGRA layout
	b0 := pixels[0]
	g0 := pixels[1]
	r0 := pixels[2]

	total := w * h
	for i := 1; i < total; i++ {
		off := i * 4
		if pixels[off] != b0 || pixels[off+1] != g0 || pixels[off+2] != r0 {
			return 0, 0, 0, false
		}
	}
	return r0, g0, b0, true
}

// colorVariance samples 16 evenly-spaced pixels and computes the sum of squared
// differences from the average for R, G, B channels.
func (t *Tight) colorVariance(pixels []byte, w, h int) float64 {
	total := w * h
	sampleCount := 16
	if total < sampleCount {
		sampleCount = total
	}

	var sumR, sumG, sumB float64
	for i := 0; i < sampleCount; i++ {
		idx := (i * total / sampleCount) * 4
		sumB += float64(pixels[idx])
		sumG += float64(pixels[idx+1])
		sumR += float64(pixels[idx+2])
	}

	n := float64(sampleCount)
	avgR := sumR / n
	avgG := sumG / n
	avgB := sumB / n

	var variance float64
	for i := 0; i < sampleCount; i++ {
		idx := (i * total / sampleCount) * 4
		dB := float64(pixels[idx]) - avgB
		dG := float64(pixels[idx+1]) - avgG
		dR := float64(pixels[idx+2]) - avgR
		variance += dR*dR + dG*dG + dB*dB
	}
	return variance
}

// encodeJPEG encodes the tile as JPEG and returns the Tight wire bytes.
func (t *Tight) encodeJPEG(pixels []byte, w, h int) ([]byte, error) {
	// Build an RGBA image from BGRA pixels
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := (y*w + x) * 4
			img.SetRGBA(x, y, color.RGBA{
				R: pixels[off+2],
				G: pixels[off+1],
				B: pixels[off],
				A: 255,
			})
		}
	}

	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, img, &jpeg.Options{Quality: t.jpegQuality}); err != nil {
		return nil, fmt.Errorf("tight jpeg encode: %w", err)
	}

	jpegData := jpegBuf.Bytes()
	cl := compactLen(len(jpegData))

	result := make([]byte, 0, 1+len(cl)+len(jpegData))
	result = append(result, 0x09) // control byte: JPEG
	result = append(result, cl...)
	result = append(result, jpegData...)
	return result, nil
}

// encodeBasic encodes the tile using zlib compression on RGB data.
func (t *Tight) encodeBasic(pixels []byte, w, h, streamIdx int) ([]byte, error) {
	stream := t.streams[streamIdx]

	// Convert BGRA to RGB
	total := w * h
	rgb := make([]byte, total*3)
	for i := 0; i < total; i++ {
		srcOff := i * 4
		dstOff := i * 3
		rgb[dstOff] = pixels[srcOff+2]   // R
		rgb[dstOff+1] = pixels[srcOff+1] // G
		rgb[dstOff+2] = pixels[srcOff]   // B
	}

	// Compress with zlib; each tile produces a complete zlib stream
	stream.buf.Reset()
	stream.writer = zlib.NewWriter(&stream.buf)

	if _, err := stream.writer.Write(rgb); err != nil {
		return nil, fmt.Errorf("tight basic zlib write: %w", err)
	}
	if err := stream.writer.Close(); err != nil {
		return nil, fmt.Errorf("tight basic zlib close: %w", err)
	}
	stream.writer = nil

	compressed := stream.buf.Bytes()
	cl := compactLen(len(compressed))

	// Control byte: bits 0-1 = stream index, bits 3-0 sub-encoding = Basic
	controlByte := byte(streamIdx & 0x03)

	result := make([]byte, 0, 1+len(cl)+len(compressed))
	result = append(result, controlByte)
	result = append(result, cl...)
	result = append(result, compressed...)
	return result, nil
}

// compactLen encodes a length value using the Tight compact length format (1-3 bytes).
func compactLen(n int) []byte {
	if n < 128 {
		return []byte{byte(n)}
	}
	if n < 16384 {
		return []byte{
			byte(n&0x7F) | 0x80,
			byte(n >> 7),
		}
	}
	return []byte{
		byte(n&0x7F) | 0x80,
		byte((n>>7)&0x7F) | 0x80,
		byte(n >> 14),
	}
}
