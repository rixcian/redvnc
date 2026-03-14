package encodings

import (
	"bytes"
	"compress/zlib"
	"io"
	"testing"

	"github.com/rixcian/redvnc/rfb"
)

func makeTestPixels(width, height int) ([]byte, int) {
	bpp := 4
	stride := width * bpp
	pixels := make([]byte, height*stride)
	// Fill with a pattern
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			offset := y*stride + x*bpp
			pixels[offset] = byte(x)   // B
			pixels[offset+1] = byte(y) // G
			pixels[offset+2] = 0       // R
			pixels[offset+3] = 255     // A
		}
	}
	return pixels, stride
}

func TestRawEncode(t *testing.T) {
	width, height := 4, 4
	pixels, stride := makeTestPixels(width, height)

	enc := &Raw{}
	if enc.Type() != rfb.EncodingRaw {
		t.Errorf("expected encoding type %d, got %d", rfb.EncodingRaw, enc.Type())
	}

	rect, err := enc.Encode(0, 0, uint16(width), uint16(height), pixels, stride)
	if err != nil {
		t.Fatalf("Raw.Encode: %v", err)
	}

	expectedLen := width * height * 4
	if len(rect.Data) != expectedLen {
		t.Errorf("expected %d bytes, got %d", expectedLen, len(rect.Data))
	}

	if rect.Header.Encoding != rfb.EncodingRaw {
		t.Errorf("expected encoding Raw, got %d", rect.Header.Encoding)
	}

	// Verify data matches source pixels
	if !bytes.Equal(rect.Data, pixels) {
		t.Error("raw encoded data doesn't match source pixels")
	}
}

func TestRawEncodeSubrect(t *testing.T) {
	width, height := 8, 8
	pixels, stride := makeTestPixels(width, height)

	enc := &Raw{}
	rect, err := enc.Encode(2, 2, 4, 4, pixels, stride)
	if err != nil {
		t.Fatalf("Raw.Encode subrect: %v", err)
	}

	expectedLen := 4 * 4 * 4
	if len(rect.Data) != expectedLen {
		t.Errorf("expected %d bytes, got %d", expectedLen, len(rect.Data))
	}

	if rect.Header.X != 2 || rect.Header.Y != 2 {
		t.Errorf("expected offset (2,2), got (%d,%d)", rect.Header.X, rect.Header.Y)
	}
}

func TestCopyRectEncode(t *testing.T) {
	enc := &CopyRect{}
	if enc.Type() != rfb.EncodingCopyRect {
		t.Errorf("expected encoding type %d, got %d", rfb.EncodingCopyRect, enc.Type())
	}

	rect := enc.EncodeCopyRect(10, 20, 100, 50, 5, 15)
	if rect.Header.X != 10 || rect.Header.Y != 20 {
		t.Errorf("expected dest (10,20), got (%d,%d)", rect.Header.X, rect.Header.Y)
	}
	if len(rect.Data) != 4 {
		t.Errorf("expected 4 bytes copyrect data, got %d", len(rect.Data))
	}
}

func TestZlibEncode(t *testing.T) {
	width, height := 4, 4
	pixels, stride := makeTestPixels(width, height)

	enc := &Zlib{}
	defer enc.Reset()

	if enc.Type() != rfb.EncodingZlib {
		t.Errorf("expected encoding type %d, got %d", rfb.EncodingZlib, enc.Type())
	}

	rect, err := enc.Encode(0, 0, uint16(width), uint16(height), pixels, stride)
	if err != nil {
		t.Fatalf("Zlib.Encode: %v", err)
	}

	if rect.Header.Encoding != rfb.EncodingZlib {
		t.Errorf("expected encoding Zlib, got %d", rect.Header.Encoding)
	}

	// Decompress and verify
	compressedLen := int(rect.Data[0])<<24 | int(rect.Data[1])<<16 | int(rect.Data[2])<<8 | int(rect.Data[3])
	compressed := rect.Data[4:]
	if len(compressed) != compressedLen {
		t.Fatalf("compressed length mismatch: header says %d, actual %d", compressedLen, len(compressed))
	}

	reader, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("zlib.NewReader: %v", err)
	}
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	reader.Close()

	expectedLen := width * height * 4
	if len(decompressed) != expectedLen {
		t.Errorf("decompressed length: expected %d, got %d", expectedLen, len(decompressed))
	}
	if !bytes.Equal(decompressed, pixels) {
		t.Error("decompressed data doesn't match source pixels")
	}
}

func TestZlibEncodeMultiple(t *testing.T) {
	// Test that the zlib encoder can be reused
	width, height := 2, 2
	pixels, stride := makeTestPixels(width, height)

	enc := &Zlib{}
	defer enc.Reset()

	for i := 0; i < 3; i++ {
		_, err := enc.Encode(0, 0, uint16(width), uint16(height), pixels, stride)
		if err != nil {
			t.Fatalf("Zlib.Encode iteration %d: %v", i, err)
		}
	}
}
