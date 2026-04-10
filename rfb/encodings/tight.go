package encodings

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"sync"

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
//
// Tiles are encoded in parallel: JPEG tiles run in independent goroutines, Basic
// tiles are distributed round-robin across the 4 zlib streams (one goroutine per
// stream, tiles within a stream encoded sequentially to preserve the zlib dictionary),
// and Solid tiles are encoded inline with no goroutine overhead.
func (t *Tight) EncodeMulti(x, y, width, height uint16, pixels []byte, stride int) ([]rfb.Rectangle, error) {
	w, h := int(width), int(height)

	// --- Phase 1: classify tiles ---
	type tileInfo struct {
		absX, absY             int
		tileW, tileH           int
		pixels                 []byte
		isSolid                bool
		solidR, solidG, solidB byte
		isJPEG                 bool
		streamIdx              int // Basic tiles only
	}

	basicCount := 0
	var tiles []tileInfo

	for tileY := 0; tileY < h; tileY += tileSize {
		tileH := min(tileSize, h-tileY)
		for tileX := 0; tileX < w; tileX += tileSize {
			tileW := min(tileSize, w-tileX)

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

			ti := tileInfo{
				absX: int(x) + tileX, absY: int(y) + tileY,
				tileW: tileW, tileH: tileH, pixels: tilePixels,
			}
			if r, g, b, ok := t.isSolidColor(tilePixels, tileW, tileH); ok {
				ti.isSolid = true
				ti.solidR, ti.solidG, ti.solidB = r, g, b
			} else if tileW*tileH >= 4096 && t.colorVariance(tilePixels, tileW, tileH) > 512 {
				ti.isJPEG = true
			} else {
				ti.streamIdx = basicCount % 4
				basicCount++
			}
			tiles = append(tiles, ti)
		}
	}

	// --- Phase 2: parallel encoding ---
	results := make([][]byte, len(tiles))
	errs := make([]error, len(tiles))
	var wg sync.WaitGroup

	// Solid: encode inline, no goroutine needed.
	for i := range tiles {
		if tiles[i].isSolid {
			ti := tiles[i]
			results[i] = []byte{0x08, ti.solidR, ti.solidG, ti.solidB}
		}
	}

	// JPEG: each tile is fully independent — one goroutine per tile.
	for i := range tiles {
		if !tiles[i].isJPEG {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = t.encodeJPEG(tiles[i].pixels, tiles[i].tileW, tiles[i].tileH)
		}(i)
	}

	// Basic: group by stream index; one goroutine per stream encodes its tiles
	// sequentially to preserve the per-stream zlib dictionary across tiles.
	// Goroutines for different streams are fully independent (each touches only
	// its own t.streams[s] entry).
	type basicWork struct {
		idx int
		ti  tileInfo
	}
	var streamWork [4][]basicWork
	for i := range tiles {
		if !tiles[i].isSolid && !tiles[i].isJPEG {
			s := tiles[i].streamIdx
			streamWork[s] = append(streamWork[s], basicWork{i, tiles[i]})
		}
	}
	for s := 0; s < 4; s++ {
		if len(streamWork[s]) == 0 {
			continue
		}
		wg.Add(1)
		go func(s int, work []basicWork) {
			defer wg.Done()
			for _, w := range work {
				data, err := t.encodeBasic(w.ti.pixels, w.ti.tileW, w.ti.tileH, s)
				results[w.idx] = data
				errs[w.idx] = err
				if err != nil {
					return
				}
			}
		}(s, streamWork[s])
	}

	wg.Wait()

	// --- Phase 3: assemble rectangles in original row-major order ---
	rects := make([]rfb.Rectangle, len(tiles))
	for i, ti := range tiles {
		if errs[i] != nil {
			return nil, fmt.Errorf("tight encode tile (%d,%d): %w", ti.absX, ti.absY, errs[i])
		}
		rects[i] = rfb.Rectangle{
			Header: rfb.RectHeader{
				X:        uint16(ti.absX),
				Y:        uint16(ti.absY),
				Width:    uint16(ti.tileW),
				Height:   uint16(ti.tileH),
				Encoding: rfb.EncodingTight,
			},
			Data: results[i],
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
// The zlib stream is persistent per stream index across tiles and frames,
// as required by the Tight encoding specification (RFC 6143 §7.7.7).
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

	// Compress with persistent zlib stream. The client maintains one
	// decompressor per stream index; we must keep using the same writer
	// so the dictionary is preserved. Flush (Z_SYNC_FLUSH) after each
	// tile so the client can decompress immediately.
	stream.buf.Reset()
	if stream.writer == nil {
		stream.writer = zlib.NewWriter(&stream.buf)
	}

	if _, err := stream.writer.Write(rgb); err != nil {
		return nil, fmt.Errorf("tight basic zlib write: %w", err)
	}
	if err := stream.writer.Flush(); err != nil {
		return nil, fmt.Errorf("tight basic zlib flush: %w", err)
	}

	compressed := stream.buf.Bytes()
	cl := compactLen(len(compressed))

	// Control byte: bits 1-0 = stream index, bits 3-2 = 00 (Basic sub-encoding)
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
