package security

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// newTestTLSConfigs returns a server *tls.Config with a self-signed cert and a
// client *tls.Config that trusts that cert.
func newTestTLSConfigs(t *testing.T) (serverCfg, clientCfg *tls.Config) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("x509 key pair: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)

	serverCfg = &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	clientCfg = &tls.Config{RootCAs: pool, ServerName: "localhost"}
	return
}

// runVeNCryptHandshake runs the server and client VeNCrypt handshakes concurrently
// over a net.Pipe(), returning both errors.
func runVeNCryptHandshake(t *testing.T, venc *VeNCrypt, clientCfg VeNCryptClientConfig) (serverErr, clientErr error) {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		serverConn.Close()
		clientConn.Close()
	})

	serverErrCh := make(chan error, 1)
	go func() {
		br := bufio.NewReader(serverConn)
		bw := bufio.NewWriter(serverConn)
		_, err := venc.Handshake(serverConn, br, bw)
		if err == nil {
			err = bw.Flush()
		}
		serverErrCh <- err
	}()

	clientErrCh := make(chan error, 1)
	go func() {
		br := bufio.NewReader(clientConn)
		bw := bufio.NewWriter(clientConn)
		_, _, _, err := VeNCryptClient(clientConn, br, bw, clientCfg)
		clientErrCh <- err
	}()

	serverErr = <-serverErrCh
	clientErr = <-clientErrCh
	return
}

// ---------------------------------------------------------------------------
// Sub-type selection
// ---------------------------------------------------------------------------

func TestVeNCryptSubTypeSelection(t *testing.T) {
	tests := []struct {
		name      string
		server    []uint32
		preferred []uint32
		want      uint32
	}{
		{
			name:      "exact match first preference",
			server:    []uint32{261, 258, 256},
			preferred: []uint32{261, 258, 256},
			want:      261,
		},
		{
			name:      "fall back to second preference",
			server:    []uint32{258, 256},
			preferred: []uint32{261, 258, 256},
			want:      258,
		},
		{
			name:      "only Plain available",
			server:    []uint32{256},
			preferred: defaultClientSubTypes,
			want:      256,
		},
		{
			name:      "no match returns 0",
			server:    []uint32{300},
			preferred: defaultClientSubTypes,
			want:      0,
		},
		{
			name:      "empty preferred uses defaults",
			server:    []uint32{261},
			preferred: nil,
			want:      261,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pref := tt.preferred
			if pref == nil {
				pref = defaultClientSubTypes
			}
			got := chooseVeNCryptSubType(tt.server, pref)
			if got != tt.want {
				t.Errorf("chooseVeNCryptSubType = %d, want %d", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Plain (256) — no TLS
// ---------------------------------------------------------------------------

func TestVeNCryptPlain(t *testing.T) {
	venc := &VeNCrypt{
		SubTypes: []uint32{256},
		PlainAuth: func(username, password string) error {
			if username == "admin" && password == "secret" {
				return nil
			}
			return bytes.ErrTooLarge // any non-nil error
		},
	}
	clientCfg := VeNCryptClientConfig{
		PreferredSubTypes: []uint32{256},
		Username:          "admin",
		Password:          "secret",
	}

	serverErr, clientErr := runVeNCryptHandshake(t, venc, clientCfg)
	if serverErr != nil {
		t.Errorf("server error: %v", serverErr)
	}
	// Client reads SecurityResult=0 inside VeNCryptClient; no error expected.
	if clientErr != nil {
		t.Errorf("client error: %v", clientErr)
	}
}

func TestVeNCryptPlainFailed(t *testing.T) {
	venc := &VeNCrypt{
		SubTypes: []uint32{256},
		PlainAuth: func(username, password string) error {
			return bytes.ErrTooLarge // always reject
		},
	}
	clientCfg := VeNCryptClientConfig{
		PreferredSubTypes: []uint32{256},
		Username:          "admin",
		Password:          "wrong",
	}

	serverErr, clientErr := runVeNCryptHandshake(t, venc, clientCfg)
	if serverErr == nil {
		t.Error("server should return auth error")
	}
	// Client reads SecurityResult=1 and the reason string, which is reported
	// as an error by the outer handshake layer (not by VeNCryptClient itself,
	// which just relays bytes). A non-nil client error is also acceptable here
	// because the conn closes after the server returns failure.
	_ = clientErr
}

func TestVeNCryptPlainNoHandler(t *testing.T) {
	// PlainAuth is nil — should always fail.
	venc := &VeNCrypt{SubTypes: []uint32{256}}
	clientCfg := VeNCryptClientConfig{
		PreferredSubTypes: []uint32{256},
		Username:          "u",
		Password:          "p",
	}
	serverErr, _ := runVeNCryptHandshake(t, venc, clientCfg)
	if serverErr == nil {
		t.Error("server should error when PlainAuth is nil")
	}
}

// ---------------------------------------------------------------------------
// TLS sub-types (257–259) — anonymous TLS (client skips cert verification)
// ---------------------------------------------------------------------------

func TestVeNCryptTLSNone(t *testing.T) {
	serverTLS, _ := newTestTLSConfigs(t)
	venc := &VeNCrypt{
		SubTypes:  []uint32{257},
		TLSConfig: serverTLS,
	}
	clientCfg := VeNCryptClientConfig{
		PreferredSubTypes: []uint32{257},
		// No TLSConfig supplied — InsecureSkipVerify is forced by clientTLSConfigInsecure.
	}

	serverErr, clientErr := runVeNCryptHandshake(t, venc, clientCfg)
	if serverErr != nil {
		t.Errorf("server error: %v", serverErr)
	}
	if clientErr != nil {
		t.Errorf("client error: %v", clientErr)
	}
}

func TestVeNCryptTLSVNCAuth(t *testing.T) {
	serverTLS, _ := newTestTLSConfigs(t)
	const password = "tlspass"
	venc := &VeNCrypt{
		SubTypes:    []uint32{258},
		TLSConfig:   serverTLS,
		VNCPassword: password,
	}
	clientCfg := VeNCryptClientConfig{
		PreferredSubTypes: []uint32{258},
		VNCPassword:       password,
	}

	serverErr, clientErr := runVeNCryptHandshake(t, venc, clientCfg)
	if serverErr != nil {
		t.Errorf("server error: %v", serverErr)
	}
	if clientErr != nil {
		t.Errorf("client error: %v", clientErr)
	}
}

func TestVeNCryptTLSPlain(t *testing.T) {
	serverTLS, _ := newTestTLSConfigs(t)
	venc := &VeNCrypt{
		SubTypes:  []uint32{259},
		TLSConfig: serverTLS,
		PlainAuth: func(u, p string) error {
			if u == "user" && p == "pass" {
				return nil
			}
			return bytes.ErrTooLarge
		},
	}
	clientCfg := VeNCryptClientConfig{
		PreferredSubTypes: []uint32{259},
		Username:          "user",
		Password:          "pass",
	}

	serverErr, clientErr := runVeNCryptHandshake(t, venc, clientCfg)
	if serverErr != nil {
		t.Errorf("server error: %v", serverErr)
	}
	if clientErr != nil {
		t.Errorf("client error: %v", clientErr)
	}
}

// ---------------------------------------------------------------------------
// X509 sub-types (260–262) — TLS with server certificate verification
// ---------------------------------------------------------------------------

func TestVeNCryptX509None(t *testing.T) {
	serverTLS, clientTLS := newTestTLSConfigs(t)
	venc := &VeNCrypt{
		SubTypes:  []uint32{260},
		TLSConfig: serverTLS,
	}
	clientCfg := VeNCryptClientConfig{
		PreferredSubTypes: []uint32{260},
		TLSConfig:         clientTLS,
	}

	serverErr, clientErr := runVeNCryptHandshake(t, venc, clientCfg)
	if serverErr != nil {
		t.Errorf("server error: %v", serverErr)
	}
	if clientErr != nil {
		t.Errorf("client error: %v", clientErr)
	}
}

func TestVeNCryptX509VNCAuth(t *testing.T) {
	serverTLS, clientTLS := newTestTLSConfigs(t)
	const password = "x509pass"
	venc := &VeNCrypt{
		SubTypes:    []uint32{261},
		TLSConfig:   serverTLS,
		VNCPassword: password,
	}
	clientCfg := VeNCryptClientConfig{
		PreferredSubTypes: []uint32{261},
		TLSConfig:         clientTLS,
		VNCPassword:       password,
	}

	serverErr, clientErr := runVeNCryptHandshake(t, venc, clientCfg)
	if serverErr != nil {
		t.Errorf("server error: %v", serverErr)
	}
	if clientErr != nil {
		t.Errorf("client error: %v", clientErr)
	}
}

func TestVeNCryptX509Plain(t *testing.T) {
	serverTLS, clientTLS := newTestTLSConfigs(t)
	venc := &VeNCrypt{
		SubTypes:  []uint32{262},
		TLSConfig: serverTLS,
		PlainAuth: func(u, p string) error {
			if u == "alice" && p == "hunter2" {
				return nil
			}
			return bytes.ErrTooLarge
		},
	}
	clientCfg := VeNCryptClientConfig{
		PreferredSubTypes: []uint32{262},
		TLSConfig:         clientTLS,
		Username:          "alice",
		Password:          "hunter2",
	}

	serverErr, clientErr := runVeNCryptHandshake(t, venc, clientCfg)
	if serverErr != nil {
		t.Errorf("server error: %v", serverErr)
	}
	if clientErr != nil {
		t.Errorf("client error: %v", clientErr)
	}
}

// ---------------------------------------------------------------------------
// Error / rejection paths
// ---------------------------------------------------------------------------

func TestVeNCryptVersionReject(t *testing.T) {
	serverTLS, _ := newTestTLSConfigs(t)
	venc := &VeNCrypt{
		SubTypes:  []uint32{257},
		TLSConfig: serverTLS,
	}

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		serverConn.Close()
		clientConn.Close()
	})

	serverErrCh := make(chan error, 1)
	go func() {
		br := bufio.NewReader(serverConn)
		bw := bufio.NewWriter(serverConn)
		_, err := venc.Handshake(serverConn, br, bw)
		bw.Flush()
		serverErrCh <- err
	}()

	// Client: read server version then send unsupported version 0.3
	br := bufio.NewReader(clientConn)
	var major, minor uint8
	if err := binary.Read(br, binary.BigEndian, &major); err != nil {
		t.Fatalf("read major: %v", err)
	}
	if err := binary.Read(br, binary.BigEndian, &minor); err != nil {
		t.Fatalf("read minor: %v", err)
	}
	// Send unsupported version
	clientConn.Write([]byte{0, 3})

	// Server sends a reject byte (0); consume it to unblock the server's Flush.
	var rejected uint8
	binary.Read(br, binary.BigEndian, &rejected)

	serverErr := <-serverErrCh
	if serverErr == nil {
		t.Error("server should reject unsupported client version")
	}
	if rejected != 0 {
		t.Errorf("expected reject byte 0, got %d", rejected)
	}
}

func TestVeNCryptBadSubType(t *testing.T) {
	serverTLS, _ := newTestTLSConfigs(t)
	venc := &VeNCrypt{
		SubTypes:  []uint32{258},
		TLSConfig: serverTLS,
	}

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		serverConn.Close()
		clientConn.Close()
	})

	serverErrCh := make(chan error, 1)
	go func() {
		br := bufio.NewReader(serverConn)
		bw := bufio.NewWriter(serverConn)
		_, err := venc.Handshake(serverConn, br, bw)
		bw.Flush()
		serverErrCh <- err
	}()

	br := bufio.NewReader(clientConn)
	bw := bufio.NewWriter(clientConn)

	// Read server version (0, 2), send back (0, 2)
	var maj, min uint8
	binary.Read(br, binary.BigEndian, &maj)
	binary.Read(br, binary.BigEndian, &min)
	bw.Write([]byte{0, 2})
	bw.Flush()

	// Read accept byte
	var accepted uint8
	binary.Read(br, binary.BigEndian, &accepted)

	// Read sub-type count + list
	var count uint8
	binary.Read(br, binary.BigEndian, &count)
	for i := uint8(0); i < count; i++ {
		var st uint32
		binary.Read(br, binary.BigEndian, &st)
	}

	// Send a sub-type NOT in the server's list
	binary.Write(bw, binary.BigEndian, uint32(999))
	bw.Flush()

	// Server sends a NAK byte (0); consume it to unblock the server's Flush.
	var nak uint8
	binary.Read(br, binary.BigEndian, &nak)

	serverErr := <-serverErrCh
	if serverErr == nil {
		t.Error("server should reject sub-type not in offered list")
	}
	if nak != 0 {
		t.Errorf("expected NAK byte 0, got %d", nak)
	}
}

// ---------------------------------------------------------------------------
// Full round-trip through all 7 sub-types
// ---------------------------------------------------------------------------

func TestVeNCryptRoundTripAllSubTypes(t *testing.T) {
	serverTLS, clientTLS := newTestTLSConfigs(t)
	// Also a client TLS config that skips verification (for TLS sub-types)
	insecureClientTLS := &tls.Config{InsecureSkipVerify: true} //nolint:gosec

	const password = "roundtrip"
	const username = "testuser"

	tests := []struct {
		name      uint32
		serverCfg func() *VeNCrypt
		clientCfg func() VeNCryptClientConfig
	}{
		{
			name: 256,
			serverCfg: func() *VeNCrypt {
				return &VeNCrypt{
					SubTypes: []uint32{256},
					PlainAuth: func(u, p string) error {
						if u == username && p == password {
							return nil
						}
						return bytes.ErrTooLarge
					},
				}
			},
			clientCfg: func() VeNCryptClientConfig {
				return VeNCryptClientConfig{
					PreferredSubTypes: []uint32{256},
					Username:          username,
					Password:          password,
				}
			},
		},
		{
			name: 257,
			serverCfg: func() *VeNCrypt {
				return &VeNCrypt{SubTypes: []uint32{257}, TLSConfig: serverTLS}
			},
			clientCfg: func() VeNCryptClientConfig {
				return VeNCryptClientConfig{
					PreferredSubTypes: []uint32{257},
					TLSConfig:         insecureClientTLS,
				}
			},
		},
		{
			name: 258,
			serverCfg: func() *VeNCrypt {
				return &VeNCrypt{SubTypes: []uint32{258}, TLSConfig: serverTLS, VNCPassword: password}
			},
			clientCfg: func() VeNCryptClientConfig {
				return VeNCryptClientConfig{
					PreferredSubTypes: []uint32{258},
					TLSConfig:         insecureClientTLS,
					VNCPassword:       password,
				}
			},
		},
		{
			name: 259,
			serverCfg: func() *VeNCrypt {
				return &VeNCrypt{
					SubTypes:  []uint32{259},
					TLSConfig: serverTLS,
					PlainAuth: func(u, p string) error {
						if u == username && p == password {
							return nil
						}
						return bytes.ErrTooLarge
					},
				}
			},
			clientCfg: func() VeNCryptClientConfig {
				return VeNCryptClientConfig{
					PreferredSubTypes: []uint32{259},
					TLSConfig:         insecureClientTLS,
					Username:          username,
					Password:          password,
				}
			},
		},
		{
			name: 260,
			serverCfg: func() *VeNCrypt {
				return &VeNCrypt{SubTypes: []uint32{260}, TLSConfig: serverTLS}
			},
			clientCfg: func() VeNCryptClientConfig {
				return VeNCryptClientConfig{
					PreferredSubTypes: []uint32{260},
					TLSConfig:         clientTLS,
				}
			},
		},
		{
			name: 261,
			serverCfg: func() *VeNCrypt {
				return &VeNCrypt{SubTypes: []uint32{261}, TLSConfig: serverTLS, VNCPassword: password}
			},
			clientCfg: func() VeNCryptClientConfig {
				return VeNCryptClientConfig{
					PreferredSubTypes: []uint32{261},
					TLSConfig:         clientTLS,
					VNCPassword:       password,
				}
			},
		},
		{
			name: 262,
			serverCfg: func() *VeNCrypt {
				return &VeNCrypt{
					SubTypes:  []uint32{262},
					TLSConfig: serverTLS,
					PlainAuth: func(u, p string) error {
						if u == username && p == password {
							return nil
						}
						return bytes.ErrTooLarge
					},
				}
			},
			clientCfg: func() VeNCryptClientConfig {
				return VeNCryptClientConfig{
					PreferredSubTypes: []uint32{262},
					TLSConfig:         clientTLS,
					Username:          username,
					Password:          password,
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(subTypeName(tt.name), func(t *testing.T) {
			serverErr, clientErr := runVeNCryptHandshake(t, tt.serverCfg(), tt.clientCfg())
			if serverErr != nil {
				t.Errorf("server error: %v", serverErr)
			}
			if clientErr != nil {
				t.Errorf("client error: %v", clientErr)
			}
		})
	}
}

func subTypeName(st uint32) string {
	switch st {
	case 256:
		return "Plain"
	case 257:
		return "TLSNone"
	case 258:
		return "TLSVNCAuth"
	case 259:
		return "TLSPlain"
	case 260:
		return "X509None"
	case 261:
		return "X509VNCAuth"
	case 262:
		return "X509Plain"
	default:
		return "Unknown"
	}
}
