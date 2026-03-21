package encodings

import (
	"bytes"
	"compress/zlib"
	"testing"

	"github.com/rixcian/redvnc/rfb"
)

func TestZRLEType(t *testing.T) {
	enc := &ZRLE{}
	if enc.Type() != rfb.EncodingZRLE {
		t.Errorf("expected encoding type %d, got %d", rfb.EncodingZRLE, enc.Type())
	}
}

func TestZRLESolidTile(t *testing.T) {
	// Create a 4x4 solid red framebuffer (BGRA)
	w, h := 4, 4
	stride := w * 4
	pixels := make([]byte, stride*h)
	for i := 0; i < w*h; i++ {
		pixels[i*4] = 0     // B
		pixels[i*4+1] = 0   // G
		pixels[i*4+2] = 255 // R
		pixels[i*4+3] = 255 // A
	}

	enc := &ZRLE{}
	rect, err := enc.Encode(0, 0, uint16(w), uint16(h), pixels, stride)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if rect.Header.Encoding != rfb.EncodingZRLE {
		t.Errorf("encoding = %d, want %d", rect.Header.Encoding, rfb.EncodingZRLE)
	}

	// Decompress and verify
	data := decompressZRLE(t, rect.Data)

	// Should be: subtype=1 (solid) + 3 bytes CPIXEL (B=0, G=0, R=255)
	if len(data) < 4 {
		t.Fatalf("decompressed too short: %d bytes", len(data))
	}
	if data[0] != 1 {
		t.Errorf("subtype = %d, want 1 (solid)", data[0])
	}
	if data[1] != 0 || data[2] != 0 || data[3] != 255 {
		t.Errorf("solid color = (%d,%d,%d), want (0,0,255)", data[1], data[2], data[3])
	}
}

func TestZRLERawTile(t *testing.T) {
	// Create a tile with many unique colors (>16) to trigger raw subencoding
	w, h := 8, 8
	stride := w * 4
	pixels := make([]byte, stride*h)
	for i := 0; i < w*h; i++ {
		pixels[i*4] = byte(i)       // B - all different
		pixels[i*4+1] = byte(i * 2) // G
		pixels[i*4+2] = byte(i * 3) // R
		pixels[i*4+3] = 255         // A
	}

	enc := &ZRLE{}
	rect, err := enc.Encode(0, 0, uint16(w), uint16(h), pixels, stride)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	data := decompressZRLE(t, rect.Data)

	// First byte should be 0 (raw subtype)
	if data[0] != 0 {
		t.Errorf("subtype = %d, want 0 (raw)", data[0])
	}

	// Remaining bytes: w*h * 3 CPIXEL bytes
	expected := w*h*3 + 1
	if len(data) != expected {
		t.Errorf("decompressed length = %d, want %d", len(data), expected)
	}
}

func TestZRLEPackedPalette(t *testing.T) {
	// Create a 4x4 tile with exactly 2 colors (checkerboard)
	w, h := 4, 4
	stride := w * 4
	pixels := make([]byte, stride*h)
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			off := (row*w + col) * 4
			if (row+col)%2 == 0 {
				pixels[off] = 255 // B
				pixels[off+1] = 0
				pixels[off+2] = 0
			} else {
				pixels[off] = 0
				pixels[off+1] = 255 // G
				pixels[off+2] = 0
			}
			pixels[off+3] = 255
		}
	}

	enc := &ZRLE{}
	rect, err := enc.Encode(0, 0, uint16(w), uint16(h), pixels, stride)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	data := decompressZRLE(t, rect.Data)

	// subtype = 2 (packed palette with 2 colors)
	if data[0] != 2 {
		t.Errorf("subtype = %d, want 2 (packed palette)", data[0])
	}
}

func TestZRLECompressesBetterThanRaw(t *testing.T) {
	// Create a 64x64 solid tile — ZRLE should compress much smaller than raw
	w, h := 64, 64
	stride := w * 4
	pixels := make([]byte, stride*h)
	for i := 0; i < len(pixels); i += 4 {
		pixels[i] = 128
		pixels[i+1] = 128
		pixels[i+2] = 128
		pixels[i+3] = 255
	}

	rawEnc := &Raw{}
	rawRect, err := rawEnc.Encode(0, 0, uint16(w), uint16(h), pixels, stride)
	if err != nil {
		t.Fatalf("Raw Encode: %v", err)
	}

	zrleEnc := &ZRLE{}
	zrleRect, err := zrleEnc.Encode(0, 0, uint16(w), uint16(h), pixels, stride)
	if err != nil {
		t.Fatalf("ZRLE Encode: %v", err)
	}

	if len(zrleRect.Data) >= len(rawRect.Data) {
		t.Errorf("ZRLE (%d bytes) should be smaller than Raw (%d bytes) for solid tile", len(zrleRect.Data), len(rawRect.Data))
	}
}

func TestZRLEMultipleTiles(t *testing.T) {
	// 128x128 — should produce 4 tiles (64x64 each)
	w, h := 128, 128
	stride := w * 4
	pixels := make([]byte, stride*h)
	for i := 0; i < len(pixels); i += 4 {
		pixels[i] = 50
		pixels[i+1] = 100
		pixels[i+2] = 150
		pixels[i+3] = 255
	}

	enc := &ZRLE{}
	rect, err := enc.Encode(0, 0, uint16(w), uint16(h), pixels, stride)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if rect.Header.Width != 128 || rect.Header.Height != 128 {
		t.Errorf("rect size = %dx%d, want 128x128", rect.Header.Width, rect.Header.Height)
	}

	// Should have compressed data
	if len(rect.Data) < 5 {
		t.Fatal("rect data too short")
	}
}

// decompressZRLE reads the 4-byte length prefix and decompresses the zlib data.
// The encoder uses Flush() (Z_SYNC_FLUSH) rather than Close(), so we read
// until the available decompressed data is exhausted rather than expecting EOF.
func decompressZRLE(t *testing.T, data []byte) []byte {
	t.Helper()
	if len(data) < 4 {
		t.Fatalf("data too short for length prefix: %d", len(data))
	}

	compressed := data[4:]
	r, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("zlib.NewReader: %v", err)
	}
	defer r.Close()

	// Read in chunks; Flush-based streams may not produce a clean EOF
	var result []byte
	buf := make([]byte, 4096)
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			result = append(result, buf[:n]...)
		}
		if readErr != nil {
			break
		}
	}

	if len(result) == 0 {
		t.Fatal("decompressed zero bytes")
	}

	return result
}
