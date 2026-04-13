package capture

import (
	"image"
	"testing"
)

func makeFrame(w, h int, fill byte) []byte {
	stride := w * 4
	pixels := make([]byte, h*stride)
	for i := range pixels {
		pixels[i] = fill
	}
	return pixels
}

func setPixel(pixels []byte, stride, x, y int, r, g, b, a byte) {
	off := y*stride + x*4
	pixels[off] = b
	pixels[off+1] = g
	pixels[off+2] = r
	pixels[off+3] = a
}

func TestFrameDiffer_FirstFrame(t *testing.T) {
	d := NewFrameDiffer()
	pixels := makeFrame(128, 128, 0)
	rects := d.Diff(pixels, 128, 128, 128*4)
	if rects != nil {
		t.Fatalf("first frame should return nil, got %v", rects)
	}
}

func TestFrameDiffer_NoChange(t *testing.T) {
	d := NewFrameDiffer()
	pixels := makeFrame(128, 128, 0)

	d.Diff(pixels, 128, 128, 128*4) // first frame

	rects := d.Diff(pixels, 128, 128, 128*4) // identical frame
	if rects == nil {
		t.Fatal("expected empty slice (no change), got nil")
	}
	if len(rects) != 0 {
		t.Fatalf("expected 0 dirty rects, got %d: %v", len(rects), rects)
	}
}

func TestFrameDiffer_SingleTileChanged(t *testing.T) {
	d := NewFrameDiffer()
	w, h := 256, 256
	stride := w * 4
	pixels := makeFrame(w, h, 0)

	d.Diff(pixels, uint16(w), uint16(h), stride) // first frame

	// Change one pixel in tile (1,0) — pixel at (70, 5)
	setPixel(pixels, stride, 70, 5, 255, 0, 0, 255)

	rects := d.Diff(pixels, uint16(w), uint16(h), stride)
	if len(rects) != 1 {
		t.Fatalf("expected 1 dirty rect, got %d: %v", len(rects), rects)
	}

	expected := image.Rect(64, 0, 128, 64)
	if rects[0] != expected {
		t.Fatalf("expected rect %v, got %v", expected, rects[0])
	}
}

func TestFrameDiffer_MultipleTilesChanged(t *testing.T) {
	d := NewFrameDiffer()
	w, h := 256, 256
	stride := w * 4
	pixels := makeFrame(w, h, 0)

	d.Diff(pixels, uint16(w), uint16(h), stride)

	// Change pixels in tiles (0,0) and (3,3)
	setPixel(pixels, stride, 0, 0, 255, 0, 0, 255)
	setPixel(pixels, stride, 200, 200, 0, 255, 0, 255)

	rects := d.Diff(pixels, uint16(w), uint16(h), stride)
	if len(rects) != 2 {
		t.Fatalf("expected 2 dirty rects, got %d: %v", len(rects), rects)
	}

	expected0 := image.Rect(0, 0, 64, 64)
	expected1 := image.Rect(192, 192, 256, 256)
	if rects[0] != expected0 {
		t.Fatalf("expected rect[0] %v, got %v", expected0, rects[0])
	}
	if rects[1] != expected1 {
		t.Fatalf("expected rect[1] %v, got %v", expected1, rects[1])
	}
}

func TestFrameDiffer_EdgeTiles(t *testing.T) {
	// 100x100 — not a multiple of 64. Edge tiles should be 36 pixels wide/tall.
	d := NewFrameDiffer()
	w, h := 100, 100
	stride := w * 4
	pixels := makeFrame(w, h, 0)

	d.Diff(pixels, uint16(w), uint16(h), stride)

	// Change pixel in the bottom-right edge tile (tile at x=64, y=64)
	setPixel(pixels, stride, 90, 90, 255, 255, 255, 255)

	rects := d.Diff(pixels, uint16(w), uint16(h), stride)
	if len(rects) != 1 {
		t.Fatalf("expected 1 dirty rect, got %d: %v", len(rects), rects)
	}

	// Edge tile: starts at (64,64), goes to (100,100)
	expected := image.Rect(64, 64, 100, 100)
	if rects[0] != expected {
		t.Fatalf("expected rect %v, got %v", expected, rects[0])
	}
}

func TestFrameDiffer_ResolutionChange(t *testing.T) {
	d := NewFrameDiffer()

	pixels1 := makeFrame(128, 128, 0)
	d.Diff(pixels1, 128, 128, 128*4)

	// Second diff at same resolution — should detect no change.
	rects := d.Diff(pixels1, 128, 128, 128*4)
	if len(rects) != 0 {
		t.Fatalf("expected no change, got %d rects", len(rects))
	}

	// Resolution change — should return nil (full frame).
	pixels2 := makeFrame(256, 256, 0)
	rects = d.Diff(pixels2, 256, 256, 256*4)
	if rects != nil {
		t.Fatalf("resolution change should return nil, got %v", rects)
	}
}

func TestFrameDiffer_FullFrameChanged(t *testing.T) {
	d := NewFrameDiffer()
	w, h := 128, 128
	stride := w * 4

	pixels := makeFrame(w, h, 0)
	d.Diff(pixels, uint16(w), uint16(h), stride)

	// Change every pixel
	pixels2 := makeFrame(w, h, 255)
	rects := d.Diff(pixels2, uint16(w), uint16(h), stride)

	// 128/64 = 2 tiles per axis → 4 tiles total
	if len(rects) != 4 {
		t.Fatalf("expected 4 dirty rects (all tiles), got %d", len(rects))
	}
}

func BenchmarkFrameDiffer_1080p_NoChange(b *testing.B) {
	d := NewFrameDiffer()
	w, h := 1920, 1080
	stride := w * 4
	pixels := makeFrame(w, h, 128)
	d.Diff(pixels, uint16(w), uint16(h), stride) // prime

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.Diff(pixels, uint16(w), uint16(h), stride)
	}
}

func BenchmarkFrameDiffer_1080p_FullChange(b *testing.B) {
	d := NewFrameDiffer()
	w, h := 1920, 1080
	stride := w * 4
	pixels := makeFrame(w, h, 0)
	d.Diff(pixels, uint16(w), uint16(h), stride) // prime

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Alternate between two different frames
		fill := byte(i % 256)
		for j := range pixels {
			pixels[j] = fill
		}
		d.Diff(pixels, uint16(w), uint16(h), stride)
	}
}
