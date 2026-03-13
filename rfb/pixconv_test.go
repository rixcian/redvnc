package rfb

import (
	"encoding/binary"
	"testing"
)

func TestConvertPixels_SameFormat(t *testing.T) {
	pf := DefaultPixelFormat()
	src := []byte{0x10, 0x20, 0x30, 0xff} // one BGRA pixel
	out := ConvertPixels(pf, pf, src, 1, 1)
	if &out[0] == &src[0] {
		// Should return same slice (no copy needed) — but at minimum, values match
	}
	for i := range src {
		if out[i] != src[i] {
			t.Fatalf("byte %d: got %02x, want %02x", i, out[i], src[i])
		}
	}
}

func TestConvertPixels_32bitBGRA_to_16bitRGB565(t *testing.T) {
	src := DefaultPixelFormat() // 32-bit BGRA, LE

	// 16-bit RGB565 big-endian (common mobile format)
	dst := PixelFormat{
		BitsPerPixel: 16,
		Depth:        16,
		BigEndian:    1,
		TrueColour:   1,
		RedMax:       31,
		GreenMax:     63,
		BlueMax:      31,
		RedShift:     11,
		GreenShift:   5,
		BlueShift:    0,
	}

	// Source pixel: B=0x00, G=0x00, R=0xFF, A=0xFF → pure red
	pixel := make([]byte, 4)
	binary.LittleEndian.PutUint32(pixel, 0xFF_FF0000) // ARGB in LE = R at shift 16
	// Actually in our BGRA format: offset 0=B, 1=G, 2=R, 3=A
	pixel[0] = 0x00 // B
	pixel[1] = 0x00 // G
	pixel[2] = 0xFF // R
	pixel[3] = 0xFF // A

	out := ConvertPixels(dst, src, pixel, 1, 1)
	if len(out) != 2 {
		t.Fatalf("expected 2 bytes, got %d", len(out))
	}

	val := binary.BigEndian.Uint16(out)
	// Red=31 at shift 11, Green=0, Blue=0 → 31<<11 = 0xF800
	if val != 0xF800 {
		t.Errorf("pure red: got 0x%04X, want 0xF800", val)
	}

	// Pure green: B=0, G=0xFF, R=0, A=0xFF
	pixel[0] = 0x00
	pixel[1] = 0xFF
	pixel[2] = 0x00
	pixel[3] = 0xFF
	out = ConvertPixels(dst, src, pixel, 1, 1)
	val = binary.BigEndian.Uint16(out)
	// Green=63 at shift 5 → 63<<5 = 0x07E0
	if val != 0x07E0 {
		t.Errorf("pure green: got 0x%04X, want 0x07E0", val)
	}

	// Pure blue: B=0xFF, G=0, R=0, A=0xFF
	pixel[0] = 0xFF
	pixel[1] = 0x00
	pixel[2] = 0x00
	pixel[3] = 0xFF
	out = ConvertPixels(dst, src, pixel, 1, 1)
	val = binary.BigEndian.Uint16(out)
	// Blue=31 at shift 0 → 0x001F
	if val != 0x001F {
		t.Errorf("pure blue: got 0x%04X, want 0x001F", val)
	}
}

func TestConvertPixels_32bit_SwapRedBlue(t *testing.T) {
	// BGRA (default) → RGBA
	src := DefaultPixelFormat() // R@16, G@8, B@0

	dst := PixelFormat{
		BitsPerPixel: 32,
		Depth:        24,
		BigEndian:    0,
		TrueColour:   1,
		RedMax:       255,
		GreenMax:     255,
		BlueMax:      255,
		RedShift:     0,
		GreenShift:   8,
		BlueShift:    16,
	}

	// Source: B=0xAA, G=0xBB, R=0xCC, A=0xFF
	pixel := []byte{0xAA, 0xBB, 0xCC, 0xFF}
	out := ConvertPixels(dst, src, pixel, 1, 1)

	got := binary.LittleEndian.Uint32(out)
	// dst: R=0xCC at shift 0, G=0xBB at shift 8, B=0xAA at shift 16
	want := uint32(0xCC) | (uint32(0xBB) << 8) | (uint32(0xAA) << 16)
	if got != want {
		t.Errorf("swap R/B: got 0x%08X, want 0x%08X", got, want)
	}
}

func TestConvertPixels_MultiplePixels(t *testing.T) {
	src := DefaultPixelFormat()
	dst := PixelFormat{
		BitsPerPixel: 16,
		Depth:        16,
		BigEndian:    0,
		TrueColour:   1,
		RedMax:       31,
		GreenMax:     63,
		BlueMax:      31,
		RedShift:     11,
		GreenShift:   5,
		BlueShift:    0,
	}

	// 2x1 pixels: red, blue
	pixels := []byte{
		0x00, 0x00, 0xFF, 0xFF, // red
		0xFF, 0x00, 0x00, 0xFF, // blue
	}

	out := ConvertPixels(dst, src, pixels, 2, 1)
	if len(out) != 4 {
		t.Fatalf("expected 4 bytes, got %d", len(out))
	}

	p0 := binary.LittleEndian.Uint16(out[0:])
	p1 := binary.LittleEndian.Uint16(out[2:])
	if p0 != 0xF800 {
		t.Errorf("pixel 0 (red): got 0x%04X, want 0xF800", p0)
	}
	if p1 != 0x001F {
		t.Errorf("pixel 1 (blue): got 0x%04X, want 0x001F", p1)
	}
}
