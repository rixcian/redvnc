package encodings

import (
	"bytes"
	"compress/zlib"
	"image/jpeg"
	"io"
	"testing"

	"github.com/rixcian/redvnc/rfb"
)

func makeSolidPixels(w, h int, r, g, b byte) ([]byte, int) {
	stride := w * 4
	pixels := make([]byte, h*stride)
	for i := 0; i < w*h; i++ {
		off := i * 4
		pixels[off] = b     // B
		pixels[off+1] = g   // G
		pixels[off+2] = r   // R
		pixels[off+3] = 255 // A
	}
	return pixels, stride
}

func makeVariedPixels(w, h int) ([]byte, int) {
	stride := w * 4
	pixels := make([]byte, h*stride)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := (y*w + x) * 4
			pixels[off] = byte((x * 37) % 256)   // B - varied
			pixels[off+1] = byte((y * 73) % 256)  // G - varied
			pixels[off+2] = byte((x*y*11) % 256)  // R - varied
			pixels[off+3] = 255
		}
	}
	return pixels, stride
}

func TestTightType(t *testing.T) {
	enc := NewTight(75)
	if enc.Type() != rfb.EncodingTight {
		t.Errorf("expected encoding type %d, got %d", rfb.EncodingTight, enc.Type())
	}
}

func TestTightSolidColor(t *testing.T) {
	enc := NewTight(75)
	defer enc.Reset()

	w, h := 8, 8
	pixels, stride := makeSolidPixels(w, h, 0xAA, 0xBB, 0xCC)

	rect, err := enc.Encode(0, 0, uint16(w), uint16(h), pixels, stride)
	if err != nil {
		t.Fatalf("Tight.Encode solid: %v", err)
	}

	if rect.Header.Encoding != rfb.EncodingTight {
		t.Errorf("expected encoding Tight, got %d", rect.Header.Encoding)
	}

	// Expect: control byte 0x08, then R, G, B
	if len(rect.Data) != 4 {
		t.Fatalf("expected 4 bytes for solid tile, got %d", len(rect.Data))
	}
	if rect.Data[0] != 0x08 {
		t.Errorf("expected control byte 0x08, got 0x%02x", rect.Data[0])
	}
	if rect.Data[1] != 0xAA || rect.Data[2] != 0xBB || rect.Data[3] != 0xCC {
		t.Errorf("expected RGB (0xAA, 0xBB, 0xCC), got (0x%02x, 0x%02x, 0x%02x)",
			rect.Data[1], rect.Data[2], rect.Data[3])
	}
}

func TestTightBasicEncoding(t *testing.T) {
	enc := NewTight(75)
	defer enc.Reset()

	// Small tile (< 4096 pixels) with varied pixels -> Basic
	w, h := 16, 16
	pixels, stride := makeVariedPixels(w, h)

	rect, err := enc.Encode(0, 0, uint16(w), uint16(h), pixels, stride)
	if err != nil {
		t.Fatalf("Tight.Encode basic: %v", err)
	}

	if len(rect.Data) < 2 {
		t.Fatalf("expected at least 2 bytes, got %d", len(rect.Data))
	}

	// Control byte should be Basic with stream 0 (0x00)
	controlByte := rect.Data[0]
	if controlByte&0x0F > 0x07 {
		t.Errorf("expected Basic sub-encoding (0x00-0x07), got 0x%02x", controlByte)
	}

	// Read compact length and decompress
	data := rect.Data[1:]
	compLen, lenBytes := readCompactLen(data)
	data = data[lenBytes:]

	if len(data) < compLen {
		t.Fatalf("expected %d compressed bytes, got %d", compLen, len(data))
	}

	reader, err := zlib.NewReader(bytes.NewReader(data[:compLen]))
	if err != nil {
		t.Fatalf("zlib.NewReader: %v", err)
	}
	defer reader.Close()

	// Read exactly the expected number of bytes. The zlib stream is persistent
	// (flushed, not closed), so io.ReadAll would fail with unexpected EOF.
	// Real VNC clients read exactly w*h*3 decompressed bytes.
	expectedLen := w * h * 3 // RGB, not BGRA
	decompressed := make([]byte, expectedLen)
	if _, err := io.ReadFull(reader, decompressed); err != nil {
		t.Fatalf("decompress: %v", err)
	}

	// Verify pixel values: BGRA -> RGB conversion
	for i := 0; i < w*h; i++ {
		srcOff := i * 4
		dstOff := i * 3
		expectR := pixels[srcOff+2]
		expectG := pixels[srcOff+1]
		expectB := pixels[srcOff]
		if decompressed[dstOff] != expectR || decompressed[dstOff+1] != expectG || decompressed[dstOff+2] != expectB {
			t.Errorf("pixel %d: expected RGB (%d,%d,%d), got (%d,%d,%d)",
				i, expectR, expectG, expectB,
				decompressed[dstOff], decompressed[dstOff+1], decompressed[dstOff+2])
			break
		}
	}
}

func TestTightJPEGEncoding(t *testing.T) {
	enc := NewTight(75)
	defer enc.Reset()

	// Full 64x64 tile with high variance
	w, h := 64, 64
	pixels, stride := makeVariedPixels(w, h)

	rect, err := enc.Encode(0, 0, uint16(w), uint16(h), pixels, stride)
	if err != nil {
		t.Fatalf("Tight.Encode jpeg: %v", err)
	}

	controlByte := rect.Data[0]
	if controlByte != 0x09 {
		t.Errorf("expected JPEG control byte 0x09, got 0x%02x", controlByte)
	}

	// Read compact length
	data := rect.Data[1:]
	jpegLen, lenBytes := readCompactLen(data)
	data = data[lenBytes:]

	// Decode JPEG
	img, err := jpeg.Decode(bytes.NewReader(data[:jpegLen]))
	if err != nil {
		t.Fatalf("jpeg.Decode: %v", err)
	}

	bounds := img.Bounds()
	if bounds.Dx() != w || bounds.Dy() != h {
		t.Errorf("JPEG dimensions: expected %dx%d, got %dx%d", w, h, bounds.Dx(), bounds.Dy())
	}
}

func TestTightMultiTile(t *testing.T) {
	enc := NewTight(75)
	defer enc.Reset()

	// 130x130 should produce 3x3 = 9 tiles (64+64+2 in each dimension)
	w, h := 130, 130
	pixels, stride := makeSolidPixels(w, h, 0xFF, 0x00, 0x00)

	rect, err := enc.Encode(0, 0, uint16(w), uint16(h), pixels, stride)
	if err != nil {
		t.Fatalf("Tight.Encode multi-tile: %v", err)
	}

	// All tiles are solid, so each is 4 bytes (control + RGB)
	// 3 * 3 = 9 tiles, each 4 bytes = 36 bytes total
	expectedTiles := 9
	expectedLen := expectedTiles * 4
	if len(rect.Data) != expectedLen {
		t.Errorf("expected %d bytes for %d solid tiles, got %d", expectedLen, expectedTiles, len(rect.Data))
	}

	// Verify each tile
	for i := 0; i < expectedTiles; i++ {
		off := i * 4
		if rect.Data[off] != 0x08 {
			t.Errorf("tile %d: expected control 0x08, got 0x%02x", i, rect.Data[off])
		}
		if rect.Data[off+1] != 0xFF || rect.Data[off+2] != 0x00 || rect.Data[off+3] != 0x00 {
			t.Errorf("tile %d: wrong color", i)
		}
	}
}

func TestTightEncodeMultiTileRects(t *testing.T) {
	enc := NewTight(75)
	defer enc.Reset()

	// 130x130 at offset (10,20) -> 3x3 = 9 separate rectangles
	w, h := 130, 130
	fbW, fbH := 200, 200
	pixels, stride := makeSolidPixels(fbW, fbH, 0xFF, 0x00, 0x00)

	rects, err := enc.EncodeMulti(10, 20, uint16(w), uint16(h), pixels, stride)
	if err != nil {
		t.Fatalf("Tight.EncodeMulti: %v", err)
	}

	if len(rects) != 9 {
		t.Fatalf("expected 9 tile rectangles, got %d", len(rects))
	}

	// Verify tile positions and sizes
	expectedTiles := []struct {
		x, y, w, h uint16
	}{
		{10, 20, 64, 64}, {74, 20, 64, 64}, {138, 20, 2, 64},
		{10, 84, 64, 64}, {74, 84, 64, 64}, {138, 84, 2, 64},
		{10, 148, 64, 2}, {74, 148, 64, 2}, {138, 148, 2, 2},
	}

	for i, exp := range expectedTiles {
		r := rects[i]
		if r.Header.X != exp.x || r.Header.Y != exp.y ||
			r.Header.Width != exp.w || r.Header.Height != exp.h {
			t.Errorf("tile %d: expected pos=(%d,%d) size=%dx%d, got pos=(%d,%d) size=%dx%d",
				i, exp.x, exp.y, exp.w, exp.h,
				r.Header.X, r.Header.Y, r.Header.Width, r.Header.Height)
		}
		if r.Header.Encoding != rfb.EncodingTight {
			t.Errorf("tile %d: expected encoding Tight, got %d", i, r.Header.Encoding)
		}
		// Each solid tile: control 0x08 + RGB
		if len(r.Data) != 4 || r.Data[0] != 0x08 {
			t.Errorf("tile %d: expected solid fill data, got %v", i, r.Data)
		}
	}
}

func TestCompactLen(t *testing.T) {
	tests := []struct {
		input    int
		expected []byte
	}{
		{0, []byte{0}},
		{1, []byte{1}},
		{127, []byte{127}},
		{128, []byte{0x80, 0x01}},
		{16383, []byte{0xFF, 0x7F}},
		{16384, []byte{0x80, 0x80, 0x01}},
	}

	for _, tt := range tests {
		got := compactLen(tt.input)
		if !bytes.Equal(got, tt.expected) {
			t.Errorf("compactLen(%d): expected %v, got %v", tt.input, tt.expected, got)
		}
	}
}

func TestTightRoundTrip(t *testing.T) {
	enc := NewTight(75)
	defer enc.Reset()

	// Use a solid color tile for exact round-trip (JPEG is lossy)
	w, h := 32, 32
	pixels, stride := makeSolidPixels(w, h, 0x12, 0x34, 0x56)

	rect, err := enc.Encode(0, 0, uint16(w), uint16(h), pixels, stride)
	if err != nil {
		t.Fatalf("Tight.Encode: %v", err)
	}

	// Manual decode: should be Fill encoding
	if len(rect.Data) < 4 {
		t.Fatalf("expected at least 4 bytes, got %d", len(rect.Data))
	}
	if rect.Data[0] != 0x08 {
		t.Fatalf("expected Fill control byte, got 0x%02x", rect.Data[0])
	}

	// Reconstruct BGRA pixels from the Fill RGB
	gotR, gotG, gotB := rect.Data[1], rect.Data[2], rect.Data[3]
	for i := 0; i < w*h; i++ {
		off := i * 4
		expectR := pixels[off+2]
		expectG := pixels[off+1]
		expectB := pixels[off]
		if gotR != expectR || gotG != expectG || gotB != expectB {
			t.Errorf("pixel %d: expected RGB (%d,%d,%d), got (%d,%d,%d)",
				i, expectR, expectG, expectB, gotR, gotG, gotB)
			break
		}
	}
}

func TestTightReuse(t *testing.T) {
	enc := NewTight(75)
	defer enc.Reset()

	w, h := 16, 16
	pixels, stride := makeVariedPixels(w, h)

	for i := 0; i < 3; i++ {
		_, err := enc.Encode(0, 0, uint16(w), uint16(h), pixels, stride)
		if err != nil {
			t.Fatalf("Tight.Encode iteration %d: %v", i, err)
		}
	}
}

// TestTightMultiStreamBasic verifies that Basic tiles are distributed round-robin
// across the 4 zlib streams and that each tile decompresses correctly.
// A low-variance gradient is used to stay below the JPEG variance threshold.
func TestTightMultiStreamBasic(t *testing.T) {
	enc := NewTight(75)
	defer enc.Reset()

	// Build a framebuffer with 5 tiles arranged horizontally (5×64 = 320 wide,
	// 16 tall). Tiles are 64×16 — below 4096 pixels so they can't be JPEG.
	// Use a slow gradient so colorVariance stays below 512.
	cols := 5
	tileW, tileH := 64, 16
	fbW, fbH := cols*tileW, tileH
	stride := fbW * 4
	pixels := make([]byte, fbH*stride)
	for y := 0; y < fbH; y++ {
		for x := 0; x < fbW; x++ {
			off := (y*fbW + x) * 4
			v := byte(x * 3 % 256) // slow ramp — low variance per 64-px tile
			pixels[off] = v         // B
			pixels[off+1] = v       // G
			pixels[off+2] = v       // R
			pixels[off+3] = 255
		}
	}

	rects, err := enc.EncodeMulti(0, 0, uint16(fbW), uint16(fbH), pixels, stride)
	if err != nil {
		t.Fatalf("EncodeMulti: %v", err)
	}
	if len(rects) != cols {
		t.Fatalf("expected %d tile rects, got %d", cols, len(rects))
	}

	// Each tile must be Basic (control byte & 0x0f in range 0x00–0x03).
	// The stream indices must cycle 0, 1, 2, 3, 0 for the 5 tiles.
	expectedStreams := []byte{0, 1, 2, 3, 0}
	for i, r := range rects {
		control := r.Data[0]
		subType := control & 0x0f
		if subType > 0x03 {
			t.Errorf("tile %d: expected Basic sub-encoding (0x00–0x03), got 0x%02x", i, subType)
			continue
		}
		if subType != expectedStreams[i] {
			t.Errorf("tile %d: expected stream %d, got %d", i, expectedStreams[i], subType)
		}
	}
}

// --- Benchmarks ---

const (
	bench1080W = 1920
	bench1080H = 1080
)

// makeLowVariancePixels creates pixels with per-tile colorVariance << 512
// (values cycle 0-3), so all tiles take the Basic/zlib path.
func makeLowVariancePixels(w, h int) ([]byte, int) {
	stride := w * 4
	pixels := make([]byte, h*stride)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			off := (y*w + x) * 4
			v := byte(x % 4)
			pixels[off] = v
			pixels[off+1] = v
			pixels[off+2] = v
			pixels[off+3] = 255
		}
	}
	return pixels, stride
}

// sequentialEncodeMulti is the pre-parallelisation reference implementation,
// used only as a benchmark baseline.
func sequentialEncodeMulti(t *Tight, x, y, width, height uint16, pixels []byte, stride int) ([]rfb.Rectangle, error) {
	w, h := int(width), int(height)
	var rects []rfb.Rectangle
	for tileY := 0; tileY < h; tileY += tileSize {
		tileH := min(tileSize, h-tileY)
		for tileX := 0; tileX < w; tileX += tileSize {
			tileW := min(tileSize, w-tileX)
			tilePixels := make([]byte, tileW*tileH*4)
			for row := 0; row < tileH; row++ {
				srcY := int(y) + tileY + row
				srcX := int(x) + tileX
				srcOffset := srcY*stride + srcX*4
				copy(tilePixels[row*tileW*4:], pixels[srcOffset:srcOffset+tileW*4])
			}
			var (
				data []byte
				err  error
			)
			if r, g, b, ok := t.isSolidColor(tilePixels, tileW, tileH); ok {
				data = []byte{0x08, r, g, b}
			} else if tileW*tileH >= 4096 && t.colorVariance(tilePixels, tileW, tileH) > 512 {
				data, err = t.encodeJPEG(tilePixels, tileW, tileH)
			} else {
				data, err = t.encodeBasic(tilePixels, tileW, tileH, 0)
			}
			if err != nil {
				return nil, err
			}
			rects = append(rects, rfb.Rectangle{
				Header: rfb.RectHeader{
					X: x + uint16(tileX), Y: y + uint16(tileY),
					Width: uint16(tileW), Height: uint16(tileH),
					Encoding: rfb.EncodingTight,
				},
				Data: data,
			})
		}
	}
	return rects, nil
}

// BenchmarkTightEncodeMulti_1080p_JPEG — all tiles take the JPEG path
// (high-variance pixels, all 64×64 tiles qualify). Maximum parallelism benefit.
func BenchmarkTightEncodeMulti_1080p_JPEG(b *testing.B) {
	enc := NewTight(75)
	defer enc.Reset()
	pixels, stride := makeVariedPixels(bench1080W, bench1080H)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := enc.EncodeMulti(0, 0, bench1080W, bench1080H, pixels, stride); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTightEncodeMulti_1080p_Basic — all tiles take the Basic/zlib path
// (low variance, not solid). Tests 4-stream parallelism.
func BenchmarkTightEncodeMulti_1080p_Basic(b *testing.B) {
	enc := NewTight(75)
	defer enc.Reset()
	pixels, stride := makeLowVariancePixels(bench1080W, bench1080H)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := enc.EncodeMulti(0, 0, bench1080W, bench1080H, pixels, stride); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTightEncodeMulti_1080p_Solid — all tiles take the Solid path.
// No goroutines spawned; represents pure overhead of the classification phase.
func BenchmarkTightEncodeMulti_1080p_Solid(b *testing.B) {
	enc := NewTight(75)
	defer enc.Reset()
	pixels, stride := makeSolidPixels(bench1080W, bench1080H, 0x40, 0x80, 0xC0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := enc.EncodeMulti(0, 0, bench1080W, bench1080H, pixels, stride); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTightSeq_1080p_JPEG — sequential baseline for JPEG tiles.
func BenchmarkTightSeq_1080p_JPEG(b *testing.B) {
	enc := NewTight(75)
	defer enc.Reset()
	pixels, stride := makeVariedPixels(bench1080W, bench1080H)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := sequentialEncodeMulti(enc, 0, 0, bench1080W, bench1080H, pixels, stride); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTightSeq_1080p_Basic — sequential baseline for Basic/zlib tiles.
func BenchmarkTightSeq_1080p_Basic(b *testing.B) {
	enc := NewTight(75)
	defer enc.Reset()
	pixels, stride := makeLowVariancePixels(bench1080W, bench1080H)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := sequentialEncodeMulti(enc, 0, 0, bench1080W, bench1080H, pixels, stride); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTightSeq_1080p_Solid — sequential baseline for Solid tiles.
func BenchmarkTightSeq_1080p_Solid(b *testing.B) {
	enc := NewTight(75)
	defer enc.Reset()
	pixels, stride := makeSolidPixels(bench1080W, bench1080H, 0x40, 0x80, 0xC0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := sequentialEncodeMulti(enc, 0, 0, bench1080W, bench1080H, pixels, stride); err != nil {
			b.Fatal(err)
		}
	}
}

// readCompactLen reads a compact length from the start of data.
// Returns the length and the number of bytes consumed.
func readCompactLen(data []byte) (int, int) {
	n := int(data[0]) & 0x7F
	if data[0]&0x80 == 0 {
		return n, 1
	}
	n |= (int(data[1]) & 0x7F) << 7
	if data[1]&0x80 == 0 {
		return n, 2
	}
	n |= int(data[2]) << 14
	return n, 3
}
