package security

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

func TestReverseBits(t *testing.T) {
	tests := []struct {
		in, out byte
	}{
		{0b10000000, 0b00000001},
		{0b11110000, 0b00001111},
		{0b10101010, 0b01010101},
		{0b00000000, 0b00000000},
		{0b11111111, 0b11111111},
	}
	for _, tt := range tests {
		got := reverseBits(tt.in)
		if got != tt.out {
			t.Errorf("reverseBits(0x%02x) = 0x%02x, want 0x%02x", tt.in, got, tt.out)
		}
	}
}

func TestVNCEncryptDecryptConsistency(t *testing.T) {
	challenge := []byte("0123456789abcdef")
	password := "secret"

	result1, err := vncEncrypt(challenge, password)
	if err != nil {
		t.Fatalf("vncEncrypt: %v", err)
	}
	result2, err := vncEncrypt(challenge, password)
	if err != nil {
		t.Fatalf("vncEncrypt: %v", err)
	}

	if !bytes.Equal(result1, result2) {
		t.Error("vncEncrypt is not deterministic")
	}

	if len(result1) != 16 {
		t.Errorf("expected 16 byte result, got %d", len(result1))
	}
}

func TestNoneHandshake(t *testing.T) {
	none := &None{}
	if none.Type() != 1 {
		t.Errorf("expected type 1, got %d", none.Type())
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	br := bufio.NewReader(serverConn)
	bw := bufio.NewWriter(serverConn)

	// Run handshake in goroutine (it writes SecurityResult)
	errCh := make(chan error, 1)
	go func() {
		_, err := none.Handshake(serverConn, br, bw)
		if err != nil {
			errCh <- err
			return
		}
		errCh <- bw.Flush()
	}()

	// Client reads SecurityResult
	var result uint32
	if err := binary.Read(clientConn, binary.BigEndian, &result); err != nil {
		t.Fatalf("read security result: %v", err)
	}
	if result != 0 {
		t.Errorf("expected security result 0, got %d", result)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("None.Handshake: %v", err)
	}
}

func TestVNCAuthType(t *testing.T) {
	auth := &VNCAuth{Password: "test"}
	if auth.Type() != 2 {
		t.Errorf("expected type 2, got %d", auth.Type())
	}
}

func TestVNCAuthHandshakeSuccess(t *testing.T) {
	password := "testpass"
	auth := &VNCAuth{Password: password}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	br := bufio.NewReader(serverConn)
	bw := bufio.NewWriter(serverConn)

	errCh := make(chan error, 1)
	go func() {
		_, err := auth.Handshake(serverConn, br, bw)
		if err != nil {
			errCh <- err
			return
		}
		errCh <- bw.Flush()
	}()

	// Simulate client: read challenge, send response
	challenge := make([]byte, 16)
	if _, err := clientConn.Read(challenge); err != nil {
		t.Fatalf("read challenge: %v", err)
	}

	response, err := vncEncrypt(challenge, password)
	if err != nil {
		t.Fatalf("vncEncrypt: %v", err)
	}
	if _, err := clientConn.Write(response); err != nil {
		t.Fatalf("write response: %v", err)
	}

	// Read SecurityResult
	var result uint32
	if err := binary.Read(clientConn, binary.BigEndian, &result); err != nil {
		t.Fatalf("read security result: %v", err)
	}
	if result != 0 {
		t.Errorf("expected security result 0, got %d", result)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("VNCAuth.Handshake: %v", err)
	}
}

func TestVNCAuthPasswordTruncation(t *testing.T) {
	// VNC passwords are truncated to 8 characters
	challenge := []byte("0123456789abcdef")

	result1, err := vncEncrypt(challenge, "longpassword")
	if err != nil {
		t.Fatalf("vncEncrypt: %v", err)
	}
	result2, err := vncEncrypt(challenge, "longpass")
	if err != nil {
		t.Fatalf("vncEncrypt: %v", err)
	}

	if !bytes.Equal(result1, result2) {
		t.Error("passwords longer than 8 chars should produce same result when truncated")
	}
}
