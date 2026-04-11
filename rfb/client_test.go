package rfb

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// mockRFBServer is a minimal RFB server for testing client behavior.
// It manages a single connection and performs protocol steps on demand.
type mockRFBServer struct {
	t    *testing.T
	ln   net.Listener
	conn net.Conn
	br   *bufio.Reader
	bw   *bufio.Writer

	// capturedSharedFlag is the ClientInit shared flag read during handshake.
	capturedSharedFlag uint8
	// capturedEncodings is the SetEncodings list sent by the client.
	capturedEncodings []int32
}

func newMockRFBServer(t *testing.T) *mockRFBServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	return &mockRFBServer{t: t, ln: ln}
}

func (ms *mockRFBServer) addr() string {
	return ms.ln.Addr().String()
}

// acceptWithDeadline waits for one connection and sets a 5 s I/O deadline.
func (ms *mockRFBServer) acceptWithDeadline() {
	ms.t.Helper()
	conn, err := ms.ln.Accept()
	if err != nil {
		ms.t.Fatalf("accept: %v", err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	ms.conn = conn
	ms.br = bufio.NewReader(conn)
	ms.bw = bufio.NewWriter(conn)
}

// refuseConnection sends numTypes=0 followed by a failure reason, then closes.
func (ms *mockRFBServer) refuseConnection(reason string) {
	ms.t.Helper()
	// Send server version first.
	ms.bw.WriteString(VersionString3_8)
	ms.bw.Flush()

	// Read (and discard) client version.
	vbuf := make([]byte, 12)
	io.ReadFull(ms.br, vbuf)

	// numTypes == 0 signals a connection failure in RFB 3.8.
	binary.Write(ms.bw, binary.BigEndian, uint8(0))
	reasonBytes := []byte(reason)
	binary.Write(ms.bw, binary.BigEndian, uint32(len(reasonBytes)))
	ms.bw.Write(reasonBytes)
	ms.bw.Flush()
	ms.conn.Close()
}

// doHandshake performs the RFB 3.8 handshake with SecurityNone and captures
// the shared flag and encoding list sent by the client.
func (ms *mockRFBServer) doHandshake() {
	ms.doHandshakeWith([]uint8{SecurityNone})
}

// doHandshakeWith performs the RFB 3.8 handshake with the given security types.
func (ms *mockRFBServer) doHandshakeWith(secTypes []uint8) {
	ms.t.Helper()

	// Send server version.
	if _, err := ms.bw.WriteString(VersionString3_8); err != nil {
		ms.t.Fatalf("send version: %v", err)
	}
	ms.bw.Flush()

	// Read client version.
	vbuf := make([]byte, 12)
	if _, err := io.ReadFull(ms.br, vbuf); err != nil {
		ms.t.Fatalf("read client version: %v", err)
	}

	// Send security type list.
	ms.bw.WriteByte(uint8(len(secTypes)))
	for _, st := range secTypes {
		ms.bw.WriteByte(st)
	}
	ms.bw.Flush()

	// Read client security choice.
	var choice uint8
	if err := binary.Read(ms.br, binary.BigEndian, &choice); err != nil {
		ms.t.Fatalf("read security choice: %v", err)
	}

	switch choice {
	case SecurityVNCAuth:
		// Send a zeroed challenge; the client encrypts it and sends back 16 bytes.
		challenge := make([]byte, 16)
		ms.bw.Write(challenge)
		ms.bw.Flush()
		response := make([]byte, 16)
		io.ReadFull(ms.br, response)
	}

	// Send SecurityResult: 0 = OK.
	binary.Write(ms.bw, binary.BigEndian, uint32(0))
	ms.bw.Flush()

	// Read ClientInit (shared flag byte).
	if err := binary.Read(ms.br, binary.BigEndian, &ms.capturedSharedFlag); err != nil {
		ms.t.Fatalf("read ClientInit: %v", err)
	}

	// Send ServerInit.
	if err := WriteServerInit(ms.bw, &ServerInit{
		Width:       800,
		Height:      600,
		PixelFormat: DefaultPixelFormat(),
		Name:        "mock",
	}); err != nil {
		ms.t.Fatalf("write ServerInit: %v", err)
	}
	ms.bw.Flush()

	// Read SetEncodings (message type + payload).
	var msgType uint8
	binary.Read(ms.br, binary.BigEndian, &msgType)
	if msgType == MsgSetEncodings {
		encs, err := ReadSetEncodings(ms.br)
		if err != nil {
			ms.t.Fatalf("read SetEncodings: %v", err)
		}
		ms.capturedEncodings = encs
	}
}

func (ms *mockRFBServer) close() {
	if ms.conn != nil {
		ms.conn.Close()
	}
}

// sendFBU sends a FramebufferUpdate with the given rectangles.
func (ms *mockRFBServer) sendFBU(rects []Rectangle) {
	ms.t.Helper()
	if err := WriteFramebufferUpdate(ms.bw, rects); err != nil {
		ms.t.Fatalf("write FBU: %v", err)
	}
	ms.bw.Flush()
}

// zlibCompress compresses data with zlib and returns the compressed bytes.
func zlibCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}
	return buf.Bytes()
}

// buildCompactLen encodes n as a Tight compact length (1–3 bytes).
func buildCompactLen(n int) []byte {
	if n < 128 {
		return []byte{byte(n)}
	}
	if n < 128*128 {
		return []byte{byte(n&0x7F) | 0x80, byte((n >> 7) & 0x7F)}
	}
	return []byte{byte(n&0x7F) | 0x80, byte((n>>7)&0x7F) | 0x80, byte(n >> 14)}
}

// ─── Handshake / connection tests ────────────────────────────────────────────

func TestClientConnectRefusedByServer(t *testing.T) {
	ms := newMockRFBServer(t)

	go func() {
		ms.acceptWithDeadline()
		ms.refuseConnection("server too busy")
	}()

	_, err := Connect(ms.addr(), ClientConfig{Shared: true})
	if err == nil {
		t.Fatal("expected error when server refuses connection, got nil")
	}
}

func TestClientSharedFlagTrue(t *testing.T) {
	ms := newMockRFBServer(t)
	done := make(chan struct{})

	go func() {
		defer close(done)
		ms.acceptWithDeadline()
		ms.doHandshake()
		ms.close()
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: true})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	<-done
	if ms.capturedSharedFlag != 1 {
		t.Errorf("expected shared flag 1, got %d", ms.capturedSharedFlag)
	}
}

func TestClientSharedFlagFalse(t *testing.T) {
	ms := newMockRFBServer(t)
	done := make(chan struct{})

	go func() {
		defer close(done)
		ms.acceptWithDeadline()
		ms.doHandshake()
		ms.close()
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: false})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	<-done
	if ms.capturedSharedFlag != 0 {
		t.Errorf("expected shared flag 0, got %d", ms.capturedSharedFlag)
	}
}

func TestClientDefaultEncodings(t *testing.T) {
	ms := newMockRFBServer(t)
	done := make(chan struct{})

	go func() {
		defer close(done)
		ms.acceptWithDeadline()
		ms.doHandshake()
		ms.close()
	}()

	// Empty Encodings → client should default to Raw only.
	client, err := Connect(ms.addr(), ClientConfig{Shared: true})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	<-done
	if len(ms.capturedEncodings) != 1 || ms.capturedEncodings[0] != EncodingRaw {
		t.Errorf("expected [Raw], got %v", ms.capturedEncodings)
	}
}

func TestClientCustomEncodings(t *testing.T) {
	ms := newMockRFBServer(t)
	done := make(chan struct{})

	go func() {
		defer close(done)
		ms.acceptWithDeadline()
		ms.doHandshake()
		ms.close()
	}()

	want := []int32{EncodingRaw, EncodingCopyRect, EncodingZlib, EncodingTight}
	client, err := Connect(ms.addr(), ClientConfig{Shared: true, Encodings: want})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	<-done
	if len(ms.capturedEncodings) != len(want) {
		t.Fatalf("expected %d encodings, got %d", len(want), len(ms.capturedEncodings))
	}
	for i, enc := range want {
		if ms.capturedEncodings[i] != enc {
			t.Errorf("encoding[%d]: expected %d, got %d", i, enc, ms.capturedEncodings[i])
		}
	}
}

func TestClientServerDimensions(t *testing.T) {
	ms := newMockRFBServer(t)

	go func() {
		ms.acceptWithDeadline()
		ms.doHandshake()
		ms.close()
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: true})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	if client.Width != 800 || client.Height != 600 {
		t.Errorf("expected 800x600, got %dx%d", client.Width, client.Height)
	}
	if client.Name != "mock" {
		t.Errorf("expected name 'mock', got '%s'", client.Name)
	}
}

// ─── ReadMessage: Bell ────────────────────────────────────────────────────────

func TestClientReadBellMessage(t *testing.T) {
	ms := newMockRFBServer(t)

	go func() {
		ms.acceptWithDeadline()
		ms.doHandshake()
		// Send Bell.
		WriteBell(ms.bw)
		ms.bw.Flush()
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: true})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	msgType, msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msgType != MsgBell {
		t.Errorf("expected MsgBell (%d), got %d", MsgBell, msgType)
	}
	if msg != nil {
		t.Errorf("expected nil payload for Bell, got %v", msg)
	}
}

// ─── ReadMessage: ServerCutText ───────────────────────────────────────────────

func TestClientReadServerCutText(t *testing.T) {
	ms := newMockRFBServer(t)
	const clipText = "hello from server clipboard"

	go func() {
		ms.acceptWithDeadline()
		ms.doHandshake()
		WriteServerCutText(ms.bw, clipText)
		ms.bw.Flush()
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: true})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	msgType, msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msgType != MsgServerCutText {
		t.Errorf("expected MsgServerCutText (%d), got %d", MsgServerCutText, msgType)
	}
	got, ok := msg.(string)
	if !ok {
		t.Fatalf("expected string payload, got %T", msg)
	}
	if got != clipText {
		t.Errorf("expected %q, got %q", clipText, got)
	}
}

func TestClientReadServerCutTextTooLarge(t *testing.T) {
	ms := newMockRFBServer(t)

	go func() {
		ms.acceptWithDeadline()
		ms.doHandshake()

		// Manually craft a ServerCutText with a huge length (>10 MB limit).
		ms.bw.WriteByte(MsgServerCutText)
		ms.bw.Write([]byte{0, 0, 0}) // padding
		binary.Write(ms.bw, binary.BigEndian, uint32(11*1024*1024))
		ms.bw.Flush()
		// Don't actually write the data — client should reject it.
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: true})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	_, _, err = client.ReadMessage()
	if err == nil {
		t.Fatal("expected error for oversized ServerCutText, got nil")
	}
}

// ─── ReadMessage: CopyRect encoding ──────────────────────────────────────────

func TestClientReadCopyRectRect(t *testing.T) {
	ms := newMockRFBServer(t)

	go func() {
		ms.acceptWithDeadline()
		ms.doHandshake()

		// CopyRect payload: 2-byte srcX + 2-byte srcY (big-endian).
		payload := make([]byte, 4)
		binary.BigEndian.PutUint16(payload[0:2], 100) // srcX
		binary.BigEndian.PutUint16(payload[2:4], 50)  // srcY

		ms.sendFBU([]Rectangle{{
			Header: RectHeader{X: 200, Y: 150, Width: 64, Height: 48, Encoding: EncodingCopyRect},
			Data:   payload,
		}})
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: true, Encodings: []int32{EncodingCopyRect}})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	msgType, msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msgType != MsgFramebufferUpdate {
		t.Fatalf("expected FramebufferUpdate, got %d", msgType)
	}
	update := msg.(*FramebufferUpdate)
	if len(update.Rects) != 1 {
		t.Fatalf("expected 1 rect, got %d", len(update.Rects))
	}
	rect := update.Rects[0]
	if rect.Encoding != EncodingCopyRect {
		t.Errorf("expected CopyRect encoding, got %d", rect.Encoding)
	}
	if len(rect.Data) != 4 {
		t.Fatalf("expected 4 bytes of CopyRect data, got %d", len(rect.Data))
	}
	srcX := binary.BigEndian.Uint16(rect.Data[0:2])
	srcY := binary.BigEndian.Uint16(rect.Data[2:4])
	if srcX != 100 || srcY != 50 {
		t.Errorf("expected srcX=100 srcY=50, got srcX=%d srcY=%d", srcX, srcY)
	}
}

// ─── ReadMessage: Zlib encoding ───────────────────────────────────────────────

func TestClientReadZlibRect(t *testing.T) {
	ms := newMockRFBServer(t)
	const w, h = 4, 4

	go func() {
		ms.acceptWithDeadline()
		ms.doHandshake()

		// Build BGRA pixel data for a 4×4 solid blue tile.
		raw := make([]byte, w*h*4)
		for i := 0; i < w*h; i++ {
			raw[i*4+0] = 255 // B
			raw[i*4+1] = 0
			raw[i*4+2] = 0
			raw[i*4+3] = 255 // A
		}
		compressed := zlibCompress(ms.t, raw)

		// Zlib wire format: 4-byte big-endian length prefix + compressed bytes.
		payload := make([]byte, 4+len(compressed))
		binary.BigEndian.PutUint32(payload[0:4], uint32(len(compressed)))
		copy(payload[4:], compressed)

		ms.sendFBU([]Rectangle{{
			Header: RectHeader{X: 0, Y: 0, Width: w, Height: h, Encoding: EncodingZlib},
			Data:   payload,
		}})
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: true, Encodings: []int32{EncodingZlib}})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	msgType, msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msgType != MsgFramebufferUpdate {
		t.Fatalf("expected FramebufferUpdate, got %d", msgType)
	}
	update := msg.(*FramebufferUpdate)
	if len(update.Rects) != 1 {
		t.Fatalf("expected 1 rect, got %d", len(update.Rects))
	}
	rect := update.Rects[0]
	if rect.Encoding != EncodingZlib {
		t.Errorf("expected Zlib encoding, got %d", rect.Encoding)
	}
	// Client stores the raw compressed bytes (including the 4-byte length prefix).
	if len(rect.Data) < 4 {
		t.Fatalf("expected at least 4 bytes of Zlib data, got %d", len(rect.Data))
	}
}

// ─── ReadMessage: Tight encoding ──────────────────────────────────────────────

func TestClientReadTightFillRect(t *testing.T) {
	ms := newMockRFBServer(t)
	// A 10×10 tile using Tight Fill sub-encoding (solid color R=255, G=128, B=64).
	const tileW, tileH = 10, 10

	go func() {
		ms.acceptWithDeadline()
		ms.doHandshake()

		// Tight tile: control byte 0x08 (Fill), then 3 bytes RGB.
		var tileBuf bytes.Buffer
		tileBuf.WriteByte(0x08)        // Fill control byte
		tileBuf.Write([]byte{255, 128, 64}) // R, G, B

		ms.sendFBU([]Rectangle{{
			Header: RectHeader{X: 0, Y: 0, Width: tileW, Height: tileH, Encoding: EncodingTight},
			Data:   tileBuf.Bytes(),
		}})
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: true, Encodings: []int32{EncodingTight}})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	msgType, msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msgType != MsgFramebufferUpdate {
		t.Fatalf("expected FramebufferUpdate, got %d", msgType)
	}
	update := msg.(*FramebufferUpdate)
	if len(update.Rects) != 1 {
		t.Fatalf("expected 1 rect, got %d", len(update.Rects))
	}
	rect := update.Rects[0]
	if rect.Encoding != EncodingTight {
		t.Errorf("expected Tight encoding, got %d", rect.Encoding)
	}

	// Client decompresses Fill into BGRA pixels: 10×10 × 4 bytes = 400 bytes.
	expectedLen := tileW * tileH * 4
	if len(rect.Data) != expectedLen {
		t.Fatalf("expected %d BGRA bytes, got %d", expectedLen, len(rect.Data))
	}

	// Verify first pixel: RGB(255,128,64) → BGRA(64,128,255,255).
	if rect.Data[0] != 64 || rect.Data[1] != 128 || rect.Data[2] != 255 || rect.Data[3] != 255 {
		t.Errorf("pixel[0] BGRA: expected [64 128 255 255], got [%d %d %d %d]",
			rect.Data[0], rect.Data[1], rect.Data[2], rect.Data[3])
	}
}

func TestClientReadTightBasicRect(t *testing.T) {
	ms := newMockRFBServer(t)
	// A 4×4 tile using Tight Basic sub-encoding (stream 0, zlib-compressed RGB).
	const tileW, tileH = 4, 4

	go func() {
		ms.acceptWithDeadline()
		ms.doHandshake()

		// Build RGB pixels: alternating red (255,0,0) and green (0,255,0).
		rgb := make([]byte, tileW*tileH*3)
		for i := 0; i < tileW*tileH; i++ {
			if i%2 == 0 {
				rgb[i*3], rgb[i*3+1], rgb[i*3+2] = 255, 0, 0
			} else {
				rgb[i*3], rgb[i*3+1], rgb[i*3+2] = 0, 255, 0
			}
		}
		compressed := zlibCompress(ms.t, rgb)

		// Tight Basic: control byte 0x00 (stream 0), compact length, compressed data.
		var tileBuf bytes.Buffer
		tileBuf.WriteByte(0x00)                           // Basic, stream 0
		tileBuf.Write(buildCompactLen(len(compressed)))   // compact-length
		tileBuf.Write(compressed)                         // zlib data

		ms.sendFBU([]Rectangle{{
			Header: RectHeader{X: 0, Y: 0, Width: tileW, Height: tileH, Encoding: EncodingTight},
			Data:   tileBuf.Bytes(),
		}})
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: true, Encodings: []int32{EncodingTight}})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	msgType, msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msgType != MsgFramebufferUpdate {
		t.Fatalf("expected FramebufferUpdate, got %d", msgType)
	}
	update := msg.(*FramebufferUpdate)
	if len(update.Rects) != 1 {
		t.Fatalf("expected 1 rect, got %d", len(update.Rects))
	}
	rect := update.Rects[0]

	// Client decompresses Tight Basic into BGRA: 4×4×4 = 64 bytes.
	expectedLen := tileW * tileH * 4
	if len(rect.Data) != expectedLen {
		t.Fatalf("expected %d BGRA bytes, got %d", expectedLen, len(rect.Data))
	}

	// Pixel 0 was RGB(255,0,0) → BGRA(0,0,255,255).
	if rect.Data[0] != 0 || rect.Data[1] != 0 || rect.Data[2] != 255 || rect.Data[3] != 255 {
		t.Errorf("pixel[0] BGRA: expected [0 0 255 255], got [%d %d %d %d]",
			rect.Data[0], rect.Data[1], rect.Data[2], rect.Data[3])
	}
	// Pixel 1 was RGB(0,255,0) → BGRA(0,255,0,255).
	if rect.Data[4] != 0 || rect.Data[5] != 255 || rect.Data[6] != 0 || rect.Data[7] != 255 {
		t.Errorf("pixel[1] BGRA: expected [0 255 0 255], got [%d %d %d %d]",
			rect.Data[4], rect.Data[5], rect.Data[6], rect.Data[7])
	}
}

// ─── SendKeyEvent wire format ─────────────────────────────────────────────────

func TestClientSendKeyEvent(t *testing.T) {
	ms := newMockRFBServer(t)

	type capturedKeyEvent struct {
		msgType  uint8
		downFlag uint8
		padding  [2]byte
		key      uint32
	}

	captured := make(chan capturedKeyEvent, 1)

	go func() {
		ms.acceptWithDeadline()
		ms.doHandshake()

		// Read the key event.
		var evt capturedKeyEvent
		binary.Read(ms.br, binary.BigEndian, &evt.msgType)
		binary.Read(ms.br, binary.BigEndian, &evt.downFlag)
		io.ReadFull(ms.br, evt.padding[:])
		binary.Read(ms.br, binary.BigEndian, &evt.key)
		captured <- evt
		ms.close()
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: true})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	const testKey = uint32(0xff0d) // Return / Enter
	if err := client.SendKeyEvent(true, testKey); err != nil {
		t.Fatalf("SendKeyEvent: %v", err)
	}

	evt := <-captured
	if evt.msgType != MsgKeyEvent {
		t.Errorf("expected msg type %d, got %d", MsgKeyEvent, evt.msgType)
	}
	if evt.downFlag != 1 {
		t.Errorf("expected down flag 1, got %d", evt.downFlag)
	}
	if evt.key != testKey {
		t.Errorf("expected key 0x%x, got 0x%x", testKey, evt.key)
	}
}

func TestClientSendKeyEventUp(t *testing.T) {
	ms := newMockRFBServer(t)
	captured := make(chan uint8, 1)

	go func() {
		ms.acceptWithDeadline()
		ms.doHandshake()

		var msgType, downFlag uint8
		binary.Read(ms.br, binary.BigEndian, &msgType)
		binary.Read(ms.br, binary.BigEndian, &downFlag)
		captured <- downFlag
		ms.close()
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: true})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	if err := client.SendKeyEvent(false, 0x41); err != nil { // 'A' key up
		t.Fatalf("SendKeyEvent: %v", err)
	}

	downFlag := <-captured
	if downFlag != 0 {
		t.Errorf("expected down flag 0 for key-up event, got %d", downFlag)
	}
}

// ─── SendPointerEvent wire format ─────────────────────────────────────────────

func TestClientSendPointerEvent(t *testing.T) {
	ms := newMockRFBServer(t)

	type capturedPointer struct {
		msgType    uint8
		buttonMask uint8
		x, y       uint16
	}

	captured := make(chan capturedPointer, 1)

	go func() {
		ms.acceptWithDeadline()
		ms.doHandshake()

		var evt capturedPointer
		binary.Read(ms.br, binary.BigEndian, &evt.msgType)
		binary.Read(ms.br, binary.BigEndian, &evt.buttonMask)
		binary.Read(ms.br, binary.BigEndian, &evt.x)
		binary.Read(ms.br, binary.BigEndian, &evt.y)
		captured <- evt
		ms.close()
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: true})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	if err := client.SendPointerEvent(0x01, 320, 240); err != nil {
		t.Fatalf("SendPointerEvent: %v", err)
	}

	evt := <-captured
	if evt.msgType != MsgPointerEvent {
		t.Errorf("expected msg type %d, got %d", MsgPointerEvent, evt.msgType)
	}
	if evt.buttonMask != 0x01 {
		t.Errorf("expected button mask 0x01, got 0x%x", evt.buttonMask)
	}
	if evt.x != 320 || evt.y != 240 {
		t.Errorf("expected (320,240), got (%d,%d)", evt.x, evt.y)
	}
}

// ─── RequestFramebufferUpdate wire format ─────────────────────────────────────

func TestClientRequestFramebufferUpdateNonIncremental(t *testing.T) {
	ms := newMockRFBServer(t)

	type capturedFBUReq struct {
		msgType     uint8
		incremental uint8
		x, y        uint16
		w, h        uint16
	}

	captured := make(chan capturedFBUReq, 1)

	go func() {
		ms.acceptWithDeadline()
		ms.doHandshake()

		var req capturedFBUReq
		binary.Read(ms.br, binary.BigEndian, &req.msgType)
		binary.Read(ms.br, binary.BigEndian, &req.incremental)
		binary.Read(ms.br, binary.BigEndian, &req.x)
		binary.Read(ms.br, binary.BigEndian, &req.y)
		binary.Read(ms.br, binary.BigEndian, &req.w)
		binary.Read(ms.br, binary.BigEndian, &req.h)
		captured <- req
		ms.close()
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: true})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	if err := client.RequestFramebufferUpdate(false, 0, 0, 800, 600); err != nil {
		t.Fatalf("RequestFramebufferUpdate: %v", err)
	}

	req := <-captured
	if req.msgType != MsgFramebufferUpdateRequest {
		t.Errorf("expected msg type %d, got %d", MsgFramebufferUpdateRequest, req.msgType)
	}
	if req.incremental != 0 {
		t.Errorf("expected incremental=0 for full update, got %d", req.incremental)
	}
	if req.x != 0 || req.y != 0 || req.w != 800 || req.h != 600 {
		t.Errorf("expected (0,0,800,600), got (%d,%d,%d,%d)", req.x, req.y, req.w, req.h)
	}
}

func TestClientRequestFramebufferUpdateIncremental(t *testing.T) {
	ms := newMockRFBServer(t)

	captured := make(chan uint8, 1)

	go func() {
		ms.acceptWithDeadline()
		ms.doHandshake()

		var msgType, incremental uint8
		binary.Read(ms.br, binary.BigEndian, &msgType)
		binary.Read(ms.br, binary.BigEndian, &incremental)
		captured <- incremental
		ms.close()
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: true})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	if err := client.RequestFramebufferUpdate(true, 0, 0, 800, 600); err != nil {
		t.Fatalf("RequestFramebufferUpdate: %v", err)
	}

	incremental := <-captured
	if incremental != 1 {
		t.Errorf("expected incremental=1, got %d", incremental)
	}
}

// ─── readCompactLen unit tests ────────────────────────────────────────────────

func TestClientReadCompactLen(t *testing.T) {
	tests := []struct {
		name  string
		bytes []byte
		want  int
	}{
		{"zero", []byte{0x00}, 0},
		{"one", []byte{0x01}, 1},
		{"127 (max 1-byte)", []byte{0x7F}, 127},
		{"128 (min 2-byte)", []byte{0x80, 0x01}, 128},
		{"255", []byte{0xFF & 0x7F | 0x80, (255 >> 7) & 0x7F}, 255},
		{"16383 (max 2-byte)", []byte{0xFF, 0x7F}, 16383},
		{"16384 (min 3-byte)", []byte{0x80, 0x80, 0x01}, 16384},
		{"65535", buildCompactLen(65535), 65535},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{br: bufio.NewReader(bytes.NewReader(tc.bytes))}
			got, err := c.readCompactLen()
			if err != nil {
				t.Fatalf("readCompactLen: %v", err)
			}
			if got != tc.want {
				t.Errorf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

// ─── chooseSecurityType unit tests ────────────────────────────────────────────

func TestClientChooseSecurityTypePreferVNCAuthWhenPasswordSet(t *testing.T) {
	c := &Client{config: ClientConfig{Password: "secret"}}
	chosen := c.chooseSecurityType([]uint8{SecurityNone, SecurityVNCAuth})
	if chosen != SecurityVNCAuth {
		t.Errorf("expected VNCAuth when password is set, got %d", chosen)
	}
}

func TestClientChooseSecurityTypeNoneWhenNoPassword(t *testing.T) {
	c := &Client{config: ClientConfig{}}
	chosen := c.chooseSecurityType([]uint8{SecurityNone, SecurityVNCAuth})
	if chosen != SecurityNone {
		t.Errorf("expected SecurityNone when no password, got %d", chosen)
	}
}

func TestClientChooseSecurityTypeFallsBackToFirstAvailable(t *testing.T) {
	// Password set but VNCAuth not available — fall back to first type.
	c := &Client{config: ClientConfig{Password: "x"}}
	// Only SecurityNone is offered; VNCAuth is not available.
	chosen := c.chooseSecurityType([]uint8{SecurityNone})
	if chosen != SecurityNone {
		t.Errorf("expected fallback to SecurityNone, got %d", chosen)
	}
}

// ─── Close ───────────────────────────────────────────────────────────────────

func TestClientCloseTwice(t *testing.T) {
	ms := newMockRFBServer(t)

	go func() {
		ms.acceptWithDeadline()
		ms.doHandshake()
		ms.close()
	}()

	client, err := Connect(ms.addr(), ClientConfig{Shared: true})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// First close should succeed.
	if err := client.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	// Second close should return an error (already closed).
	if err := client.Close(); err == nil {
		t.Error("expected error on second Close, got nil")
	}
}
