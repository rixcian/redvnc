// Package security implements RFB security types (None, VNC Authentication, and VeNCrypt).
package security

import (
	"bufio"
	"crypto/des"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

// SecurityType defines the interface for RFB security handlers.
type SecurityType interface {
	// Type returns the RFB security type number.
	Type() uint8
	// Handshake performs the server-side security handshake.
	// It may return a TLS-upgraded net.Conn (e.g. VeNCrypt). Non-upgrading
	// handlers return conn unchanged. The caller must replace its conn/br/bw
	// if the returned conn differs from the input conn.
	Handshake(conn net.Conn, br *bufio.Reader, bw *bufio.Writer) (net.Conn, error)
}

// None implements SecurityType with no authentication.
type None struct{}

func (n *None) Type() uint8 { return 1 }

func (n *None) Handshake(conn net.Conn, br *bufio.Reader, bw *bufio.Writer) (net.Conn, error) {
	// SecurityResult: OK
	err := binary.Write(bw, binary.BigEndian, uint32(0))
	return conn, err
}

// VNCAuth implements SecurityType with VNC DES challenge-response authentication.
type VNCAuth struct {
	Password string
}

func (v *VNCAuth) Type() uint8 { return 2 }

func (v *VNCAuth) Handshake(conn net.Conn, br *bufio.Reader, bw *bufio.Writer) (net.Conn, error) {
	return conn, vncAuthHandshake(br, bw, v.Password)
}

// vncAuthHandshake performs the server-side VNC DES challenge-response handshake,
// writing the SecurityResult. It is reused by VeNCrypt inner auth.
func vncAuthHandshake(br *bufio.Reader, bw *bufio.Writer, password string) error {
	// Generate 16-byte random challenge
	challenge := make([]byte, 16)
	if _, err := rand.Read(challenge); err != nil {
		return fmt.Errorf("generate challenge: %w", err)
	}

	// Send challenge and flush so the client can read it
	if _, err := bw.Write(challenge); err != nil {
		return fmt.Errorf("send challenge: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("flush challenge: %w", err)
	}

	// Read 16-byte response
	response := make([]byte, 16)
	if _, err := io.ReadFull(br, response); err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	// Verify response
	expected, err := vncEncrypt(challenge, password)
	if err != nil {
		return fmt.Errorf("vnc auth encrypt: %w", err)
	}
	ok := true
	for i := range expected {
		if expected[i] != response[i] {
			ok = false
		}
	}

	if !ok {
		// SecurityResult: Failed
		if err := binary.Write(bw, binary.BigEndian, uint32(1)); err != nil {
			return err
		}
		reason := "authentication failed"
		if err := binary.Write(bw, binary.BigEndian, uint32(len(reason))); err != nil {
			return err
		}
		if _, err := bw.Write([]byte(reason)); err != nil {
			return err
		}
		return fmt.Errorf("vnc auth: %s", reason)
	}

	// SecurityResult: OK
	return binary.Write(bw, binary.BigEndian, uint32(0))
}

// vncEncrypt encrypts a 16-byte challenge using the VNC DES scheme.
// The password is truncated/padded to 8 bytes, each byte is bit-reversed,
// then used as the DES key to encrypt the challenge.
func vncEncrypt(challenge []byte, password string) ([]byte, error) {
	key := make([]byte, 8)
	copy(key, []byte(password))

	// VNC uses reversed bit order for each byte of the key
	for i := range key {
		key[i] = reverseBits(key[i])
	}

	cipher, err := des.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("des.NewCipher: %w", err)
	}

	result := make([]byte, 16)
	cipher.Encrypt(result[:8], challenge[:8])
	cipher.Encrypt(result[8:], challenge[8:])
	return result, nil
}

// reverseBits reverses the bit order of a byte.
func reverseBits(b byte) byte {
	var result byte
	for i := 0; i < 8; i++ {
		result = (result << 1) | (b & 1)
		b >>= 1
	}
	return result
}

// VNCAuthClient performs the client side of VNC authentication.
func VNCAuthClient(rw io.ReadWriter, password string) error {
	// Read 16-byte challenge
	challenge := make([]byte, 16)
	if _, err := io.ReadFull(rw, challenge); err != nil {
		return fmt.Errorf("read challenge: %w", err)
	}

	// Send encrypted response
	response, err := vncEncrypt(challenge, password)
	if err != nil {
		return fmt.Errorf("vnc auth encrypt: %w", err)
	}
	if _, err := rw.Write(response); err != nil {
		return fmt.Errorf("send response: %w", err)
	}

	return nil
}
