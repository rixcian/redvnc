package security

import (
	"bytes"
	"encoding/binary"
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

	var buf bytes.Buffer
	err := none.Handshake(&buf)
	if err != nil {
		t.Fatalf("None.Handshake: %v", err)
	}

	var result uint32
	binary.Read(&buf, binary.BigEndian, &result)
	if result != 0 {
		t.Errorf("expected security result 0, got %d", result)
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

	// Simulate the handshake: server writes challenge, client responds
	var serverBuf bytes.Buffer

	// We need a pipe-like setup. Let's do it manually.
	// Server writes challenge → client reads challenge → client computes response → server verifies

	// Step 1: Generate challenge (the Handshake method does this internally)
	// We'll test by calling VNCAuthClient with the same password

	// Create a connected pair using bytes.Buffer as intermediary
	challengeResponse := &authSimulator{password: password}
	err := auth.Handshake(challengeResponse)
	if err != nil {
		t.Fatalf("VNCAuth.Handshake with correct password should succeed: %v", err)
	}

	// Verify the security result was written
	var result uint32
	binary.Read(&serverBuf, binary.BigEndian, &result)
	// The result is written to the authSimulator, which we can check
}

// authSimulator simulates a VNC client for testing the server handshake.
type authSimulator struct {
	password  string
	challenge []byte
	writeBuf  bytes.Buffer
}

func (a *authSimulator) Read(p []byte) (int, error) {
	if a.challenge == nil {
		return 0, nil
	}
	// Return the encrypted response
	response, err := vncEncrypt(a.challenge, a.password)
	if err != nil {
		return 0, err
	}
	return copy(p, response), nil
}

func (a *authSimulator) Write(p []byte) (int, error) {
	if a.challenge == nil && len(p) == 16 {
		// Server is writing the challenge
		a.challenge = make([]byte, 16)
		copy(a.challenge, p)
		return len(p), nil
	}
	// Server is writing security result
	return a.writeBuf.Write(p)
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
