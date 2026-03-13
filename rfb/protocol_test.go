package rfb

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestDefaultPixelFormat(t *testing.T) {
	pf := DefaultPixelFormat()
	if pf.BitsPerPixel != 32 {
		t.Errorf("expected 32 bpp, got %d", pf.BitsPerPixel)
	}
	if pf.Depth != 24 {
		t.Errorf("expected depth 24, got %d", pf.Depth)
	}
	if pf.TrueColour != 1 {
		t.Errorf("expected true colour, got %d", pf.TrueColour)
	}
	if pf.RedMax != 255 || pf.GreenMax != 255 || pf.BlueMax != 255 {
		t.Errorf("expected max 255 for all channels")
	}
}

func TestWriteServerInit(t *testing.T) {
	var buf bytes.Buffer
	init := &ServerInit{
		Width:       1024,
		Height:      768,
		PixelFormat: DefaultPixelFormat(),
		Name:        "test",
	}

	err := WriteServerInit(&buf, init)
	if err != nil {
		t.Fatalf("WriteServerInit: %v", err)
	}

	// Read back and verify
	data := buf.Bytes()
	width := binary.BigEndian.Uint16(data[0:2])
	height := binary.BigEndian.Uint16(data[2:4])
	if width != 1024 || height != 768 {
		t.Errorf("expected 1024x768, got %dx%d", width, height)
	}

	// Name length at offset 2+2+16 = 20
	nameLen := binary.BigEndian.Uint32(data[20:24])
	if nameLen != 4 {
		t.Errorf("expected name length 4, got %d", nameLen)
	}
	name := string(data[24:28])
	if name != "test" {
		t.Errorf("expected name 'test', got '%s'", name)
	}
}

func TestReadFramebufferUpdateRequest(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint8(1))   // incremental
	binary.Write(&buf, binary.BigEndian, uint16(10))  // x
	binary.Write(&buf, binary.BigEndian, uint16(20))  // y
	binary.Write(&buf, binary.BigEndian, uint16(100)) // width
	binary.Write(&buf, binary.BigEndian, uint16(200)) // height

	req, err := ReadFramebufferUpdateRequest(&buf)
	if err != nil {
		t.Fatalf("ReadFramebufferUpdateRequest: %v", err)
	}
	if req.Incremental != 1 {
		t.Errorf("expected incremental=1, got %d", req.Incremental)
	}
	if req.X != 10 || req.Y != 20 {
		t.Errorf("expected x=10 y=20, got x=%d y=%d", req.X, req.Y)
	}
	if req.Width != 100 || req.Height != 200 {
		t.Errorf("expected 100x200, got %dx%d", req.Width, req.Height)
	}
}

func TestReadKeyEvent(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint8(1))      // down
	buf.Write([]byte{0, 0})                              // padding
	binary.Write(&buf, binary.BigEndian, uint32(0xff0d)) // Enter key

	evt, err := ReadKeyEvent(&buf)
	if err != nil {
		t.Fatalf("ReadKeyEvent: %v", err)
	}
	if evt.DownFlag != 1 {
		t.Errorf("expected down=1, got %d", evt.DownFlag)
	}
	if evt.Key != 0xff0d {
		t.Errorf("expected key=0xff0d, got %x", evt.Key)
	}
}

func TestReadPointerEvent(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint8(1))    // button mask
	binary.Write(&buf, binary.BigEndian, uint16(500)) // x
	binary.Write(&buf, binary.BigEndian, uint16(300)) // y

	evt, err := ReadPointerEvent(&buf)
	if err != nil {
		t.Fatalf("ReadPointerEvent: %v", err)
	}
	if evt.ButtonMask != 1 {
		t.Errorf("expected buttonMask=1, got %d", evt.ButtonMask)
	}
	if evt.X != 500 || evt.Y != 300 {
		t.Errorf("expected 500,300 got %d,%d", evt.X, evt.Y)
	}
}

func TestWriteFramebufferUpdate(t *testing.T) {
	var buf bytes.Buffer
	pixels := make([]byte, 4*4) // 2x2 pixels, 4 bytes each

	rects := []Rectangle{
		{
			Header: RectHeader{
				X: 0, Y: 0, Width: 2, Height: 2,
				Encoding: EncodingRaw,
			},
			Data: pixels,
		},
	}

	err := WriteFramebufferUpdate(&buf, rects)
	if err != nil {
		t.Fatalf("WriteFramebufferUpdate: %v", err)
	}

	data := buf.Bytes()
	if data[0] != MsgFramebufferUpdate {
		t.Errorf("expected msg type %d, got %d", MsgFramebufferUpdate, data[0])
	}
	numRects := binary.BigEndian.Uint16(data[2:4])
	if numRects != 1 {
		t.Errorf("expected 1 rect, got %d", numRects)
	}
}

func TestReadSetEncodings(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(0) // padding
	binary.Write(&buf, binary.BigEndian, uint16(3))
	binary.Write(&buf, binary.BigEndian, int32(EncodingRaw))
	binary.Write(&buf, binary.BigEndian, int32(EncodingCopyRect))
	binary.Write(&buf, binary.BigEndian, int32(EncodingZlib))

	encs, err := ReadSetEncodings(&buf)
	if err != nil {
		t.Fatalf("ReadSetEncodings: %v", err)
	}
	if len(encs) != 3 {
		t.Fatalf("expected 3 encodings, got %d", len(encs))
	}
	if encs[0] != EncodingRaw || encs[1] != EncodingCopyRect || encs[2] != EncodingZlib {
		t.Errorf("unexpected encodings: %v", encs)
	}
}

func TestReadClientCutText(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 0}) // padding
	text := "hello clipboard"
	binary.Write(&buf, binary.BigEndian, uint32(len(text)))
	buf.WriteString(text)

	result, err := ReadClientCutText(&buf)
	if err != nil {
		t.Fatalf("ReadClientCutText: %v", err)
	}
	if result != text {
		t.Errorf("expected '%s', got '%s'", text, result)
	}
}

func TestWriteBell(t *testing.T) {
	var buf bytes.Buffer
	err := WriteBell(&buf)
	if err != nil {
		t.Fatalf("WriteBell: %v", err)
	}
	if buf.Len() != 1 || buf.Bytes()[0] != MsgBell {
		t.Errorf("expected bell message byte")
	}
}
