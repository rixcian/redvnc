package rfb

import (
	"net"
	"testing"
	"time"

	"github.com/rixcian/redvnc/rfb/security"
)

func TestIntegrationVNCAuth(t *testing.T) {
	password := "secret123"
	server := NewServer(ServerConfig{
		Width:  800,
		Height: 600,
		Name:   "auth-test",
		Security: []SecurityHandler{
			&security.VNCAuth{Password: password},
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

	// Connect with correct password
	client, err := Connect(ln.Addr().String(), ClientConfig{
		Shared:   true,
		Password: password,
	})
	if err != nil {
		t.Fatalf("Connect with correct password: %v", err)
	}
	defer client.Close()

	if client.Width != 800 || client.Height != 600 {
		t.Errorf("expected 800x600, got %dx%d", client.Width, client.Height)
	}
}

func TestIntegrationVNCAuthBadPassword(t *testing.T) {
	server := NewServer(ServerConfig{
		Width:  800,
		Height: 600,
		Name:   "auth-fail-test",
		Security: []SecurityHandler{
			&security.VNCAuth{Password: "correct"},
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

	// Connect with wrong password — should fail
	_, err = Connect(ln.Addr().String(), ClientConfig{
		Shared:   true,
		Password: "wrong",
	})
	if err == nil {
		t.Fatal("expected error for wrong password, got nil")
	}
}

func TestIntegrationClientDisconnect(t *testing.T) {
	server := NewServer(ServerConfig{
		Width:  640,
		Height: 480,
		Name:   "disconnect-test",
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

	// Connect and immediately disconnect
	for i := 0; i < 5; i++ {
		client, err := Connect(ln.Addr().String(), ClientConfig{Shared: true})
		if err != nil {
			t.Fatalf("Connect %d: %v", i, err)
		}
		client.Close()
	}

	// Wait for disconnections to propagate
	time.Sleep(200 * time.Millisecond)

	// Server should still be healthy — no panic, no leaked goroutines
	server.mu.Lock()
	remaining := len(server.clients)
	server.mu.Unlock()
	if remaining != 0 {
		t.Errorf("expected 0 remaining clients, got %d", remaining)
	}
}
