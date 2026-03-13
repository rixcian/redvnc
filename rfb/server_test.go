package rfb

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
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
