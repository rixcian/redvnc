package wsproxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rixcian/redvnc/rfb"
	"nhooyr.io/websocket"
)

func TestConnectionManager(t *testing.T) {
	cm := NewConnectionManager(3, 2)

	s1 := &Session{ID: "1", Target: "10.0.0.1:5900"}
	s2 := &Session{ID: "2", Target: "10.0.0.1:5900"}
	s3 := &Session{ID: "3", Target: "10.0.0.2:5900"}
	s4 := &Session{ID: "4", Target: "10.0.0.1:5900"}
	s5 := &Session{ID: "5", Target: "10.0.0.3:5900"}

	// Should succeed
	if !cm.TryAdd(s1) {
		t.Fatal("should accept s1")
	}
	if !cm.TryAdd(s2) {
		t.Fatal("should accept s2")
	}
	if !cm.TryAdd(s3) {
		t.Fatal("should accept s3")
	}

	// s4 should fail: max per target (2 for 10.0.0.1:5900)
	if cm.TryAdd(s4) {
		t.Fatal("should reject s4 (per-target limit)")
	}

	// s5 should fail: max total connections (3)
	if cm.TryAdd(s5) {
		t.Fatal("should reject s5 (total limit)")
	}

	// Remove s1, then s4 should succeed (frees one slot for both total and target)
	cm.Remove(s1)
	if !cm.TryAdd(s4) {
		t.Fatal("should accept s4 after removing s1")
	}

	if cm.Count() != 3 {
		t.Fatalf("expected 3 sessions, got %d", cm.Count())
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"test.txt", "test.txt", false},
		{"../../../etc/passwd", "passwd", false},
		{"my file.txt", "my file.txt", false},
		{"file\x00name.txt", "filename.txt", false},
		{"/path/to/file.txt", "file.txt", false},
		{".", "", true},
		{"..", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		got, err := SanitizeFilename(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("SanitizeFilename(%q): expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("SanitizeFilename(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestUniqueFilePath(t *testing.T) {
	dir := t.TempDir()

	// First file should be as-is
	path := UniqueFilePath(dir, "test.txt")
	if filepath.Base(path) != "test.txt" {
		t.Fatalf("expected test.txt, got %s", filepath.Base(path))
	}

	// Create the file, next should be test(1).txt
	os.WriteFile(path, []byte("hello"), 0644)
	path2 := UniqueFilePath(dir, "test.txt")
	if filepath.Base(path2) != "test(1).txt" {
		t.Fatalf("expected test(1).txt, got %s", filepath.Base(path2))
	}

	// Create that too, next should be test(2).txt
	os.WriteFile(path2, []byte("hello"), 0644)
	path3 := UniqueFilePath(dir, "test.txt")
	if filepath.Base(path3) != "test(2).txt" {
		t.Fatalf("expected test(2).txt, got %s", filepath.Base(path3))
	}
}

func TestIsUploadDirAllowed(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	os.MkdirAll(subdir, 0755)

	s := &Server{
		config: Config{
			DefaultUploadDir: dir,
		},
		allowedUpDirs: []string{dir},
	}

	if !s.IsUploadDirAllowed(dir) {
		t.Error("should allow exact match")
	}
	if !s.IsUploadDirAllowed(subdir) {
		t.Error("should allow subdirectory")
	}
	if s.IsUploadDirAllowed("/tmp/other") {
		t.Error("should reject directory outside allowed list")
	}
}

func TestSessionInit(t *testing.T) {
	init := &rfb.ServerInit{
		Width:  1024,
		Height: 768,
		PixelFormat: rfb.PixelFormat{
			BitsPerPixel: 32,
			Depth:        24,
			BigEndian:    0,
			TrueColour:   1,
			RedMax:       255,
			GreenMax:     255,
			BlueMax:      255,
			RedShift:     16,
			GreenShift:   8,
			BlueShift:    0,
		},
		NameLength: 4,
		Name:       "test",
	}

	nameBytes := []byte(init.Name)
	payloadLen := 2 + 2 + 16 + 4 + len(nameBytes)
	buf := make([]byte, 5+payloadLen)
	buf[0] = ExtSessionInit
	binary.BigEndian.PutUint32(buf[1:5], uint32(payloadLen))
	binary.BigEndian.PutUint16(buf[5:7], init.Width)
	binary.BigEndian.PutUint16(buf[7:9], init.Height)

	// Verify the message structure
	if buf[0] != 128 {
		t.Errorf("expected type 128, got %d", buf[0])
	}
	if binary.BigEndian.Uint16(buf[5:7]) != 1024 {
		t.Errorf("expected width 1024, got %d", binary.BigEndian.Uint16(buf[5:7]))
	}
	if binary.BigEndian.Uint16(buf[7:9]) != 768 {
		t.Errorf("expected height 768, got %d", binary.BigEndian.Uint16(buf[7:9]))
	}
}

func TestUploadStatus(t *testing.T) {
	// Verify UploadStatus message encoding
	uploadID := uint32(42)
	status := uint8(0)
	bytesWritten := uint64(1024)
	message := "upload started"
	msgBytes := []byte(message)
	payloadLen := 4 + 1 + 8 + 2 + len(msgBytes)

	buf := make([]byte, 5+payloadLen)
	buf[0] = ExtUploadStatus
	binary.BigEndian.PutUint32(buf[1:5], uint32(payloadLen))
	binary.BigEndian.PutUint32(buf[5:9], uploadID)
	buf[9] = status
	binary.BigEndian.PutUint64(buf[10:18], bytesWritten)
	binary.BigEndian.PutUint16(buf[18:20], uint16(len(msgBytes)))
	copy(buf[20:], msgBytes)

	// Verify
	if buf[0] != 134 {
		t.Errorf("expected type 134, got %d", buf[0])
	}
	if binary.BigEndian.Uint32(buf[5:9]) != 42 {
		t.Errorf("expected uploadID 42, got %d", binary.BigEndian.Uint32(buf[5:9]))
	}
	if buf[9] != 0 {
		t.Errorf("expected status 0, got %d", buf[9])
	}
	if binary.BigEndian.Uint64(buf[10:18]) != 1024 {
		t.Errorf("expected bytesWritten 1024, got %d", binary.BigEndian.Uint64(buf[10:18]))
	}
}

// fakeVNCServer creates a minimal VNC server for testing the handshake.
func fakeVNCServer(t *testing.T, listener net.Listener) {
	t.Helper()
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	// Send version
	conn.Write([]byte(rfb.VersionString3_8))

	// Read client version
	ver := make([]byte, 12)
	conn.Read(ver)

	// Send security types: just None
	conn.Write([]byte{1, 1}) // 1 type, type 1 (None)

	// Read security choice
	choice := make([]byte, 1)
	conn.Read(choice)

	// Send SecurityResult: OK
	binary.Write(conn, binary.BigEndian, uint32(0))

	// Read ClientInit
	shared := make([]byte, 1)
	conn.Read(shared)

	// Send ServerInit
	init := &rfb.ServerInit{
		Width:       800,
		Height:      600,
		PixelFormat: rfb.DefaultPixelFormat(),
		Name:        "test-vnc",
	}
	rfb.WriteServerInit(conn, init)

	// Keep connection open, relay any data back
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		conn.Write(buf[:n])
	}
}

func TestWebSocketProxy(t *testing.T) {
	// Start a fake VNC server
	vncListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer vncListener.Close()
	go fakeVNCServer(t, vncListener)

	vncAddr := vncListener.Addr().String()

	// Create proxy server
	config := Config{
		ListenAddr:              ":0",
		AllowedVNCTargets:       []string{vncAddr},
		MaxConnections:          10,
		MaxConnectionsPerTarget: 5,
		DefaultUploadDir:        t.TempDir(),
	}
	server := NewServer(config)

	// Use httptest for testing
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", server.handleWebSocket)
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	// Connect via WebSocket
	wsURL := "ws" + httpServer.URL[4:] + fmt.Sprintf("/ws?target=%s", vncAddr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	// Read SessionInit message
	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("read session init: %v", err)
	}

	if data[0] != ExtSessionInit {
		t.Fatalf("expected SessionInit (128), got %d", data[0])
	}

	width := binary.BigEndian.Uint16(data[5:7])
	height := binary.BigEndian.Uint16(data[7:9])
	if width != 800 || height != 600 {
		t.Fatalf("expected 800x600, got %dx%d", width, height)
	}

	// Verify name from SessionInit
	off := 9 + 16 // skip pixel format
	nameLen := binary.BigEndian.Uint32(data[off : off+4])
	name := string(data[off+4 : off+4+int(nameLen)])
	if name != "test-vnc" {
		t.Fatalf("expected name 'test-vnc', got '%s'", name)
	}
}

func TestTargetNotAllowed(t *testing.T) {
	config := Config{
		ListenAddr:        ":0",
		AllowedVNCTargets: []string{"10.0.0.1:5900"},
	}
	server := NewServer(config)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", server.handleWebSocket)
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	// Try connecting to a disallowed target
	wsURL := "ws" + httpServer.URL[4:] + "/ws?target=10.0.0.99:5900"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, wsURL, nil)
	if err == nil {
		t.Fatal("expected error for disallowed target")
	}
	if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestMissingTarget(t *testing.T) {
	config := Config{ListenAddr: ":0"}
	server := NewServer(config)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", server.handleWebSocket)
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	wsURL := "ws" + httpServer.URL[4:] + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, wsURL, nil)
	if err == nil {
		t.Fatal("expected error for missing target")
	}
	if resp != nil && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := NewSession("127.0.0.1:12345", "10.0.0.1:5900", "pass", nil)

	if s.ID == "" {
		t.Fatal("session ID should not be empty")
	}
	if s.ClientAddr != "127.0.0.1:12345" {
		t.Fatalf("unexpected client addr: %s", s.ClientAddr)
	}
	if s.Target != "10.0.0.1:5900" {
		t.Fatalf("unexpected target: %s", s.Target)
	}

	// Test upload tracking
	u := &uploadState{ID: 1, FileName: "test.txt"}
	s.AddUpload(u)
	if s.ActiveUploadCount() != 1 {
		t.Fatal("expected 1 active upload")
	}
	if s.GetUpload(1) == nil {
		t.Fatal("expected to find upload 1")
	}
	if s.GetUpload(99) != nil {
		t.Fatal("expected nil for unknown upload")
	}

	s.RemoveUpload(1)
	if s.ActiveUploadCount() != 0 {
		t.Fatal("expected 0 active uploads")
	}
}

func TestDefaultDownloadsDir(t *testing.T) {
	dir := defaultDownloadsDir()
	if dir == "" {
		t.Fatal("default downloads dir should not be empty")
	}
}
