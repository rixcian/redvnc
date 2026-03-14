package rfb

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"
)

func TestServerHandshakeNoAuth(t *testing.T) {
	server := NewServer(ServerConfig{
		Width:  800,
		Height: 600,
		Name:   "test-server",
	})

	// Start server on a random port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		server.handleConnection(conn)
	}()

	// Connect as client
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Read server version
	version := make([]byte, 12)
	if _, err := io.ReadFull(conn, version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if string(version) != VersionString3_8 {
		t.Fatalf("expected version %q, got %q", VersionString3_8, string(version))
	}

	// Send client version
	if _, err := conn.Write([]byte(VersionString3_8)); err != nil {
		t.Fatalf("write version: %v", err)
	}

	// Read security types
	var numTypes uint8
	if err := binary.Read(conn, binary.BigEndian, &numTypes); err != nil {
		t.Fatalf("read security type count: %v", err)
	}
	if numTypes < 1 {
		t.Fatal("expected at least 1 security type")
	}

	secTypes := make([]uint8, numTypes)
	for i := range secTypes {
		binary.Read(conn, binary.BigEndian, &secTypes[i])
	}

	// Choose None
	found := false
	for _, st := range secTypes {
		if st == SecurityNone {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("SecurityNone not offered")
	}
	binary.Write(conn, binary.BigEndian, uint8(SecurityNone))

	// Read security result
	var secResult uint32
	if err := binary.Read(conn, binary.BigEndian, &secResult); err != nil {
		t.Fatalf("read security result: %v", err)
	}
	if secResult != 0 {
		t.Fatalf("expected security result 0, got %d", secResult)
	}

	// Send ClientInit (shared=1)
	binary.Write(conn, binary.BigEndian, uint8(1))

	// Read ServerInit
	var width, height uint16
	binary.Read(conn, binary.BigEndian, &width)
	binary.Read(conn, binary.BigEndian, &height)
	if width != 800 || height != 600 {
		t.Errorf("expected 800x600, got %dx%d", width, height)
	}

	// Skip pixel format (16 bytes)
	pf := make([]byte, 16)
	io.ReadFull(conn, pf)

	// Read name
	var nameLen uint32
	binary.Read(conn, binary.BigEndian, &nameLen)
	name := make([]byte, nameLen)
	io.ReadFull(conn, name)
	if string(name) != "test-server" {
		t.Errorf("expected name 'test-server', got '%s'", string(name))
	}
}

func TestServerClientIntegration(t *testing.T) {
	server := NewServer(ServerConfig{
		Width:  640,
		Height: 480,
		Name:   "integration-test",
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go server.handleConnection(conn)
		}
	}()

	// Connect using the Client
	addr := ln.Addr().String()
	client, err := Connect(addr, ClientConfig{Shared: true})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	if client.Width != 640 || client.Height != 480 {
		t.Errorf("expected 640x480, got %dx%d", client.Width, client.Height)
	}
	if client.Name != "integration-test" {
		t.Errorf("expected name 'integration-test', got '%s'", client.Name)
	}

	// Request a framebuffer update
	err = client.RequestFramebufferUpdate(false, 0, 0, 640, 480)
	if err != nil {
		t.Fatalf("RequestFramebufferUpdate: %v", err)
	}

	// Read the response
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
	if rect.Width != 640 || rect.Height != 480 {
		t.Errorf("expected rect 640x480, got %dx%d", rect.Width, rect.Height)
	}
	expectedLen := 640 * 480 * 4
	if len(rect.Data) != expectedLen {
		t.Errorf("expected %d bytes of pixel data, got %d", expectedLen, len(rect.Data))
	}
}

func TestServerMultipleClients(t *testing.T) {
	server := NewServer(ServerConfig{
		Width:  320,
		Height: 240,
		Name:   "multi-client",
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go server.handleConnection(conn)
		}
	}()

	addr := ln.Addr().String()

	// Connect multiple clients
	clients := make([]*Client, 3)
	for i := range clients {
		c, err := Connect(addr, ClientConfig{Shared: true})
		if err != nil {
			t.Fatalf("Connect client %d: %v", i, err)
		}
		clients[i] = c
	}

	// Verify all connected
	for i, c := range clients {
		if c.Width != 320 || c.Height != 240 {
			t.Errorf("client %d: expected 320x240, got %dx%d", i, c.Width, c.Height)
		}
	}

	// Verify server tracks all clients
	server.mu.Lock()
	numClients := len(server.clients)
	server.mu.Unlock()
	if numClients != 3 {
		t.Errorf("expected 3 clients tracked, got %d", numClients)
	}

	// Close all clients
	for _, c := range clients {
		c.Close()
	}

	// Wait briefly for disconnections to propagate
	time.Sleep(100 * time.Millisecond)

	server.mu.Lock()
	numClients = len(server.clients)
	server.mu.Unlock()
	fmt.Printf("remaining clients after close: %d\n", numClients)
}

// staticCursor implements CursorProvider with a fixed cursor image.
type staticCursor struct {
	cursor *CursorImage
}

func (s *staticCursor) Cursor() *CursorImage { return s.cursor }

// resizableCapturer implements ScreenCapturer with a changeable size.
type resizableCapturer struct {
	mu     sync.Mutex
	width  uint16
	height uint16
}

func (r *resizableCapturer) Bounds() (uint16, uint16) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.width, r.height
}

func (r *resizableCapturer) Capture() ([]byte, int, error) {
	r.mu.Lock()
	w, h := r.width, r.height
	r.mu.Unlock()
	stride := int(w) * 4
	return make([]byte, stride*int(h)), stride, nil
}

func (r *resizableCapturer) Resize(w, h uint16) {
	r.mu.Lock()
	r.width = w
	r.height = h
	r.mu.Unlock()
}

func TestCursorPseudoEncoding(t *testing.T) {
	cursor := &CursorImage{
		Width:    2,
		Height:   2,
		HotspotX: 1,
		HotspotY: 1,
		Pixels:   make([]byte, 2*2*4),
		Mask:     []byte{0xC0, 0xC0},
	}

	cap := &resizableCapturer{width: 640, height: 480}
	server := NewServer(ServerConfig{
		Capturer:       cap,
		Name:           "cursor-test",
		CursorProvider: &staticCursor{cursor: cursor},
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go server.handleConnection(conn)
		}
	}()

	client, err := Connect(ln.Addr().String(), ClientConfig{
		Shared:    true,
		Encodings: []int32{EncodingRaw, EncodingCursor},
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	// Request framebuffer update — should include cursor pseudo-rect
	err = client.RequestFramebufferUpdate(false, 0, 0, 640, 480)
	if err != nil {
		t.Fatalf("RequestFramebufferUpdate: %v", err)
	}

	msgType, msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msgType != MsgFramebufferUpdate {
		t.Fatalf("expected FramebufferUpdate, got %d", msgType)
	}

	update := msg.(*FramebufferUpdate)
	// Should have cursor rect + framebuffer rect
	if len(update.Rects) < 2 {
		t.Fatalf("expected at least 2 rects (cursor + framebuffer), got %d", len(update.Rects))
	}

	// Find the cursor rect
	foundCursor := false
	for _, rect := range update.Rects {
		if rect.Encoding == EncodingCursor {
			foundCursor = true
			if rect.X != 1 || rect.Y != 1 {
				t.Errorf("cursor hotspot: expected (1,1), got (%d,%d)", rect.X, rect.Y)
			}
			if rect.Width != 2 || rect.Height != 2 {
				t.Errorf("cursor size: expected 2x2, got %dx%d", rect.Width, rect.Height)
			}
			// Data should contain pixels (2*2*4=16 bytes) + mask (2 bytes) = 18 bytes
			expectedLen := 2*2*4 + 2
			if len(rect.Data) != expectedLen {
				t.Errorf("cursor data: expected %d bytes, got %d", expectedLen, len(rect.Data))
			}
			break
		}
	}
	if !foundCursor {
		t.Error("no cursor pseudo-encoding rect found in framebuffer update")
	}
}

func TestDesktopResizePseudoEncoding(t *testing.T) {
	cap := &resizableCapturer{width: 640, height: 480}
	server := NewServer(ServerConfig{
		Capturer: cap,
		Name:     "resize-test",
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go server.handleConnection(conn)
		}
	}()

	client, err := Connect(ln.Addr().String(), ClientConfig{
		Shared:    true,
		Encodings: []int32{EncodingRaw, EncodingDesktopSize},
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	if client.Width != 640 || client.Height != 480 {
		t.Fatalf("expected initial 640x480, got %dx%d", client.Width, client.Height)
	}

	// First request at original size — should be normal
	err = client.RequestFramebufferUpdate(false, 0, 0, 640, 480)
	if err != nil {
		t.Fatalf("RequestFramebufferUpdate: %v", err)
	}
	_, _, err = client.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage (first): %v", err)
	}

	// Resize the screen
	cap.Resize(1024, 768)

	// Next request should include a DesktopSize pseudo-rect
	err = client.RequestFramebufferUpdate(false, 0, 0, 640, 480)
	if err != nil {
		t.Fatalf("RequestFramebufferUpdate after resize: %v", err)
	}

	msgType, msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage after resize: %v", err)
	}
	if msgType != MsgFramebufferUpdate {
		t.Fatalf("expected FramebufferUpdate, got %d", msgType)
	}

	update := msg.(*FramebufferUpdate)
	foundResize := false
	for _, rect := range update.Rects {
		if rect.Encoding == EncodingDesktopSize {
			foundResize = true
			if rect.Width != 1024 || rect.Height != 768 {
				t.Errorf("desktop resize: expected 1024x768, got %dx%d", rect.Width, rect.Height)
			}
			if client.Width != 1024 || client.Height != 768 {
				t.Errorf("client should have updated dimensions to 1024x768, got %dx%d", client.Width, client.Height)
			}
			break
		}
	}
	if !foundResize {
		t.Error("no DesktopSize pseudo-encoding rect found after resize")
	}
}

func TestNotifyDesktopResize(t *testing.T) {
	server := NewServer(ServerConfig{
		Width:  640,
		Height: 480,
		Name:   "notify-resize-test",
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go server.handleConnection(conn)
		}
	}()

	client, err := Connect(ln.Addr().String(), ClientConfig{
		Shared:    true,
		Encodings: []int32{EncodingRaw, EncodingDesktopSize},
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	// Give the server a moment to register the client
	time.Sleep(50 * time.Millisecond)

	// Push a desktop resize notification
	server.NotifyDesktopResize(1920, 1080)

	// Read the notification
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
	if rect.Encoding != EncodingDesktopSize {
		t.Fatalf("expected DesktopSize encoding, got %d", rect.Encoding)
	}
	if rect.Width != 1920 || rect.Height != 1080 {
		t.Errorf("expected 1920x1080, got %dx%d", rect.Width, rect.Height)
	}
	if client.Width != 1920 || client.Height != 1080 {
		t.Errorf("client dimensions should be 1920x1080, got %dx%d", client.Width, client.Height)
	}
}

// generateSelfSignedCert creates a self-signed TLS certificate for testing.
func generateSelfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate key: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(1 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}, nil
}

func TestTLSConnection(t *testing.T) {
	cert, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("generate cert: %v", err)
	}

	server := NewServer(ServerConfig{
		Width:  1024,
		Height: 768,
		Name:   "tls-test-server",
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
		},
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go server.handleConnection(conn)
		}
	}()

	addr := ln.Addr().String()

	// Connect with TLS client
	client, err := Connect(addr, ClientConfig{
		Shared: true,
		TLSConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	})
	if err != nil {
		t.Fatalf("Connect with TLS: %v", err)
	}
	defer client.Close()

	if client.Width != 1024 || client.Height != 768 {
		t.Errorf("expected 1024x768, got %dx%d", client.Width, client.Height)
	}
	if client.Name != "tls-test-server" {
		t.Errorf("expected name 'tls-test-server', got '%s'", client.Name)
	}

	// Verify we can exchange data over the TLS connection
	err = client.RequestFramebufferUpdate(false, 0, 0, client.Width, client.Height)
	if err != nil {
		t.Fatalf("RequestFramebufferUpdate: %v", err)
	}

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
}

func TestTLSConnectionFailsWithoutTLSClient(t *testing.T) {
	cert, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("generate cert: %v", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	// Accept one connection on the server side with a TLS wrapper and deadline
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Set a deadline so the TLS handshake doesn't block forever
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		tlsConn := tls.Server(conn, tlsConfig)
		// The TLS handshake should fail because the client sends plain text
		_ = tlsConn.Handshake()
	}()

	// Connect WITHOUT TLS — should fail during handshake.
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(2 * time.Second))

	// Send a plain-text RFB version string (not TLS)
	_, _ = conn.Write([]byte(VersionString3_8))

	// Try to read — should get garbage or error since the server is speaking TLS
	buf := make([]byte, 12)
	_, err = io.ReadFull(conn, buf)
	if err == nil {
		// If we got data, it should not be a valid RFB version string
		if string(buf) == VersionString3_8 {
			t.Fatal("expected TLS server to not respond with plain RFB version")
		}
	}
	// Either an error or non-RFB data confirms the plain client can't talk to a TLS server

	// Wait for server goroutine to finish
	<-serverDone
}
