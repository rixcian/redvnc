package security

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

// VeNCrypt version supported by this implementation.
const (
	veNCryptMajor = 0
	veNCryptMinor = 2
)

// Default sub-type preference order for the server (most secure first).
var defaultServerSubTypes = []uint32{
	258, // TLSVNCAuth
	261, // X509VNCAuth
	259, // TLSPlain
	262, // X509Plain
	257, // TLSNone
	260, // X509None
	256, // Plain
}

// Default sub-type preference order for the client (most secure first).
var defaultClientSubTypes = []uint32{
	261, // X509VNCAuth
	258, // TLSVNCAuth
	262, // X509Plain
	259, // TLSPlain
	260, // X509None
	257, // TLSNone
	256, // Plain
}

// VeNCrypt implements the VeNCrypt (RFB security type 19) server-side handler.
// It offers a list of sub-types to the client; each may involve a TLS upgrade
// followed by an inner authentication step.
type VeNCrypt struct {
	// SubTypes is the ordered list of sub-types offered to clients.
	// If empty, defaults to [TLSVNCAuth, X509VNCAuth, TLSPlain, X509Plain, TLSNone, X509None, Plain].
	SubTypes []uint32

	// TLSConfig is used for TLS sub-types (257–262). Must have a certificate
	// loaded. For TLS sub-types (257–259) the client uses InsecureSkipVerify;
	// for X509 sub-types (260–262) the client verifies the server certificate.
	// The server's TLSConfig is the same in both cases.
	TLSConfig *tls.Config

	// VNCPassword is used for VNCAuth inner auth (sub-types 258, 261).
	VNCPassword string

	// PlainAuth is called to verify username+password for Plain inner auth
	// (sub-types 256, 259, 262). Return nil to accept, an error to reject.
	// If nil, Plain sub-types will always reject.
	PlainAuth func(username, password string) error
}

// Type returns the VeNCrypt security type number (19).
func (v *VeNCrypt) Type() uint8 { return 19 }

// Handshake performs the server-side VeNCrypt handshake.
// It returns a TLS-upgraded conn for TLS/X509 sub-types, or the original conn
// for the Plain sub-type.
func (v *VeNCrypt) Handshake(conn net.Conn, br *bufio.Reader, bw *bufio.Writer) (net.Conn, error) {
	subTypes := v.SubTypes
	if len(subTypes) == 0 {
		subTypes = defaultServerSubTypes
	}

	// Step 1: Send VeNCrypt version (major=0, minor=2).
	if _, err := bw.Write([]byte{veNCryptMajor, veNCryptMinor}); err != nil {
		return conn, fmt.Errorf("vencrypt: send version: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return conn, fmt.Errorf("vencrypt: flush version: %w", err)
	}

	// Step 2: Read client version.
	var clientMajor, clientMinor uint8
	if err := binary.Read(br, binary.BigEndian, &clientMajor); err != nil {
		return conn, fmt.Errorf("vencrypt: read client version major: %w", err)
	}
	if err := binary.Read(br, binary.BigEndian, &clientMinor); err != nil {
		return conn, fmt.Errorf("vencrypt: read client version minor: %w", err)
	}

	// Step 3: Accept or reject the client version.
	if clientMajor != veNCryptMajor || clientMinor != veNCryptMinor {
		_ = bw.WriteByte(0) // reject
		_ = bw.Flush()
		return conn, fmt.Errorf("vencrypt: unsupported client version %d.%d", clientMajor, clientMinor)
	}
	if err := bw.WriteByte(1); err != nil { // accept
		return conn, fmt.Errorf("vencrypt: send accept: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return conn, fmt.Errorf("vencrypt: flush accept: %w", err)
	}

	// Step 4: Send sub-type list.
	if len(subTypes) > 255 {
		subTypes = subTypes[:255]
	}
	if err := bw.WriteByte(uint8(len(subTypes))); err != nil {
		return conn, fmt.Errorf("vencrypt: send subtype count: %w", err)
	}
	for _, st := range subTypes {
		if err := binary.Write(bw, binary.BigEndian, st); err != nil {
			return conn, fmt.Errorf("vencrypt: send subtype: %w", err)
		}
	}
	if err := bw.Flush(); err != nil {
		return conn, fmt.Errorf("vencrypt: flush subtypes: %w", err)
	}

	// Step 5: Read client's chosen sub-type.
	var chosen uint32
	if err := binary.Read(br, binary.BigEndian, &chosen); err != nil {
		return conn, fmt.Errorf("vencrypt: read chosen subtype: %w", err)
	}

	// Validate the chosen sub-type is in our offered list.
	valid := false
	for _, st := range subTypes {
		if st == chosen {
			valid = true
			break
		}
	}
	if !valid {
		_ = bw.WriteByte(0) // NAK
		_ = bw.Flush()
		return conn, fmt.Errorf("vencrypt: client chose sub-type %d not in offered list", chosen)
	}

	// Step 6: Send ACK (1 = OK).
	if err := bw.WriteByte(1); err != nil {
		return conn, fmt.Errorf("vencrypt: send ack: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return conn, fmt.Errorf("vencrypt: flush ack: %w", err)
	}

	// Step 7: Perform TLS upgrade (if needed) and inner auth.
	return v.performSubType(conn, br, bw, chosen)
}

func (v *VeNCrypt) performSubType(conn net.Conn, br *bufio.Reader, bw *bufio.Writer, subType uint32) (net.Conn, error) {
	switch subType {
	case 256: // Plain — no TLS
		return conn, v.performPlainAuth(br, bw)

	case 257, 258, 259: // TLS sub-types (anonymous: client skips cert verification)
		upgConn, newBR, newBW, err := upgradeTLSServer(conn, br, bw, v.TLSConfig)
		if err != nil {
			return conn, fmt.Errorf("vencrypt: tls upgrade: %w", err)
		}
		return upgConn, v.performInnerAuth(newBR, newBW, subType)

	case 260, 261, 262: // X509 sub-types (client verifies server certificate)
		upgConn, newBR, newBW, err := upgradeTLSServer(conn, br, bw, v.TLSConfig)
		if err != nil {
			return conn, fmt.Errorf("vencrypt: x509 tls upgrade: %w", err)
		}
		return upgConn, v.performInnerAuth(newBR, newBW, subType)

	default:
		return conn, fmt.Errorf("vencrypt: unhandled sub-type %d", subType)
	}
}

func (v *VeNCrypt) performInnerAuth(br *bufio.Reader, bw *bufio.Writer, subType uint32) error {
	switch subType {
	case 257, 260: // TLSNone, X509None — no inner auth, send SecurityResult OK
		err := binary.Write(bw, binary.BigEndian, uint32(0))
		if err != nil {
			return err
		}
		return bw.Flush()

	case 258, 261: // TLSVNCAuth, X509VNCAuth
		if err := vncAuthHandshake(br, bw, v.VNCPassword); err != nil {
			return err
		}
		return bw.Flush()

	case 259, 262: // TLSPlain, X509Plain
		return v.performPlainAuth(br, bw)

	default:
		return fmt.Errorf("vencrypt: unknown inner sub-type %d", subType)
	}
}

func (v *VeNCrypt) performPlainAuth(br *bufio.Reader, bw *bufio.Writer) error {
	// Read username: uint16 length + bytes.
	var ulen uint16
	if err := binary.Read(br, binary.BigEndian, &ulen); err != nil {
		return fmt.Errorf("vencrypt plain: read username len: %w", err)
	}
	username := make([]byte, ulen)
	if _, err := io.ReadFull(br, username); err != nil {
		return fmt.Errorf("vencrypt plain: read username: %w", err)
	}

	// Read password: uint16 length + bytes.
	var plen uint16
	if err := binary.Read(br, binary.BigEndian, &plen); err != nil {
		return fmt.Errorf("vencrypt plain: read password len: %w", err)
	}
	password := make([]byte, plen)
	if _, err := io.ReadFull(br, password); err != nil {
		return fmt.Errorf("vencrypt plain: read password: %w", err)
	}

	// Verify credentials.
	var authErr error
	if v.PlainAuth != nil {
		authErr = v.PlainAuth(string(username), string(password))
	} else {
		authErr = fmt.Errorf("no PlainAuth handler configured")
	}

	if authErr != nil {
		// SecurityResult: Failed
		_ = binary.Write(bw, binary.BigEndian, uint32(1))
		reason := authErr.Error()
		_ = binary.Write(bw, binary.BigEndian, uint32(len(reason)))
		_, _ = bw.Write([]byte(reason))
		_ = bw.Flush()
		return fmt.Errorf("vencrypt plain auth: %w", authErr)
	}

	// SecurityResult: OK
	if err := binary.Write(bw, binary.BigEndian, uint32(0)); err != nil {
		return err
	}
	return bw.Flush()
}

// upgradeTLSServer wraps conn in TLS (server side) and returns a new conn
// along with fresh bufio.Reader/Writer over the TLS connection.
// Any bytes already buffered in br are re-injected into the new reader.
func upgradeTLSServer(conn net.Conn, br *bufio.Reader, bw *bufio.Writer, tlsCfg *tls.Config) (net.Conn, *bufio.Reader, *bufio.Writer, error) {
	// Flush any pending writes before wrapping.
	if err := bw.Flush(); err != nil {
		return nil, nil, nil, fmt.Errorf("vencrypt tls server: flush before upgrade: %w", err)
	}

	// Drain bytes already buffered in br (should be 0 after VeNCrypt framing,
	// but guard against TCP over-delivery).
	preread := drainBuffered(br)

	tlsConn := tls.Server(conn, tlsCfg)
	if err := tlsConn.Handshake(); err != nil {
		return nil, nil, nil, fmt.Errorf("vencrypt tls server: handshake: %w", err)
	}

	newBR := newReaderWithPreread(tlsConn, preread)
	newBW := bufio.NewWriterSize(tlsConn, 256*1024)
	return tlsConn, newBR, newBW, nil
}

// upgradeTLSClient wraps conn in TLS (client side) and returns a new conn
// along with fresh bufio.Reader/Writer over the TLS connection.
func upgradeTLSClient(conn net.Conn, br *bufio.Reader, bw *bufio.Writer, tlsCfg *tls.Config) (net.Conn, *bufio.Reader, *bufio.Writer, error) {
	// Flush any pending writes before wrapping.
	if err := bw.Flush(); err != nil {
		return nil, nil, nil, fmt.Errorf("vencrypt tls client: flush before upgrade: %w", err)
	}

	preread := drainBuffered(br)

	tlsConn := tls.Client(conn, tlsCfg)
	if err := tlsConn.Handshake(); err != nil {
		return nil, nil, nil, fmt.Errorf("vencrypt tls client: handshake: %w", err)
	}

	newBR := newReaderWithPreread(tlsConn, preread)
	newBW := bufio.NewWriterSize(tlsConn, 256*1024)
	return tlsConn, newBR, newBW, nil
}

// drainBuffered reads all bytes currently buffered in br without blocking.
func drainBuffered(br *bufio.Reader) []byte {
	n := br.Buffered()
	if n == 0 {
		return nil
	}
	buf := make([]byte, n)
	_, _ = io.ReadFull(br, buf)
	return buf
}

// newReaderWithPreread builds a bufio.Reader over r, prepending any pre-read bytes.
func newReaderWithPreread(r io.Reader, preread []byte) *bufio.Reader {
	if len(preread) > 0 {
		return bufio.NewReaderSize(io.MultiReader(bytes.NewReader(preread), r), 256*1024)
	}
	return bufio.NewReaderSize(r, 256*1024)
}

// VeNCryptClientConfig holds client-side VeNCrypt configuration.
type VeNCryptClientConfig struct {
	// PreferredSubTypes lists sub-types in order of preference.
	// If empty, defaults to [X509VNCAuth, TLSVNCAuth, X509Plain, TLSPlain, X509None, TLSNone, Plain].
	PreferredSubTypes []uint32

	// TLSConfig is the base TLS config for TLS/X509 sub-types.
	// For TLS sub-types (257–259), InsecureSkipVerify is forced true on a clone.
	// For X509 sub-types (260–262), this config is used as-is.
	// If nil, a minimal config is constructed.
	TLSConfig *tls.Config

	// VNCPassword is the password for VNCAuth inner auth (sub-types 258, 261).
	VNCPassword string

	// Username is the username for Plain inner auth (sub-types 256, 259, 262).
	Username string

	// Password is the password for Plain inner auth (sub-types 256, 259, 262).
	Password string
}

// VeNCryptClient performs the client-side VeNCrypt handshake.
// It returns the (possibly TLS-upgraded) conn, new bufio.Reader, and new
// bufio.Writer. The caller must replace its conn/br/bw with the returned values.
func VeNCryptClient(conn net.Conn, br *bufio.Reader, bw *bufio.Writer, cfg VeNCryptClientConfig) (net.Conn, *bufio.Reader, *bufio.Writer, error) {
	// Step 1: Read server version.
	var major, minor uint8
	if err := binary.Read(br, binary.BigEndian, &major); err != nil {
		return conn, br, bw, fmt.Errorf("vencrypt client: read version major: %w", err)
	}
	if err := binary.Read(br, binary.BigEndian, &minor); err != nil {
		return conn, br, bw, fmt.Errorf("vencrypt client: read version minor: %w", err)
	}

	// Step 2: Send our version.
	if err := bw.WriteByte(veNCryptMajor); err != nil {
		return conn, br, bw, fmt.Errorf("vencrypt client: send version major: %w", err)
	}
	if err := bw.WriteByte(veNCryptMinor); err != nil {
		return conn, br, bw, fmt.Errorf("vencrypt client: send version minor: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return conn, br, bw, fmt.Errorf("vencrypt client: flush version: %w", err)
	}

	// Step 3: Read accept/reject.
	var accepted uint8
	if err := binary.Read(br, binary.BigEndian, &accepted); err != nil {
		return conn, br, bw, fmt.Errorf("vencrypt client: read accept: %w", err)
	}
	if accepted == 0 {
		return conn, br, bw, fmt.Errorf("vencrypt client: server rejected version %d.%d", major, minor)
	}

	// Step 4: Read server's sub-type list.
	var numSubTypes uint8
	if err := binary.Read(br, binary.BigEndian, &numSubTypes); err != nil {
		return conn, br, bw, fmt.Errorf("vencrypt client: read subtype count: %w", err)
	}
	serverSubTypes := make([]uint32, numSubTypes)
	for i := range serverSubTypes {
		if err := binary.Read(br, binary.BigEndian, &serverSubTypes[i]); err != nil {
			return conn, br, bw, fmt.Errorf("vencrypt client: read subtype %d: %w", i, err)
		}
	}

	// Step 5: Choose a sub-type and send it.
	preferred := cfg.PreferredSubTypes
	if len(preferred) == 0 {
		preferred = defaultClientSubTypes
	}
	chosen := chooseVeNCryptSubType(serverSubTypes, preferred)
	if chosen == 0 {
		return conn, br, bw, fmt.Errorf("vencrypt client: no compatible sub-type (server offers %v)", serverSubTypes)
	}
	if err := binary.Write(bw, binary.BigEndian, chosen); err != nil {
		return conn, br, bw, fmt.Errorf("vencrypt client: send chosen subtype: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return conn, br, bw, fmt.Errorf("vencrypt client: flush chosen subtype: %w", err)
	}

	// Step 6: Read ACK.
	var ack uint8
	if err := binary.Read(br, binary.BigEndian, &ack); err != nil {
		return conn, br, bw, fmt.Errorf("vencrypt client: read ack: %w", err)
	}
	if ack == 0 {
		return conn, br, bw, fmt.Errorf("vencrypt client: server rejected sub-type %d", chosen)
	}

	// Step 7: Perform TLS upgrade (if needed) and inner auth.
	return performVeNCryptClientSubType(conn, br, bw, chosen, cfg)
}

func performVeNCryptClientSubType(conn net.Conn, br *bufio.Reader, bw *bufio.Writer, subType uint32, cfg VeNCryptClientConfig) (net.Conn, *bufio.Reader, *bufio.Writer, error) {
	switch subType {
	case 256: // Plain — no TLS
		if err := sendPlainAuth(bw, cfg.Username, cfg.Password); err != nil {
			return conn, br, bw, err
		}
		if err := bw.Flush(); err != nil {
			return conn, br, bw, err
		}
		return conn, br, bw, readVeNCryptSecurityResult(br)

	case 257, 258, 259: // TLS sub-types: skip server certificate verification
		tlsCfg := clientTLSConfigInsecure(cfg.TLSConfig)
		upgConn, newBR, newBW, err := upgradeTLSClient(conn, br, bw, tlsCfg)
		if err != nil {
			return conn, br, bw, err
		}
		return upgConn, newBR, newBW, performClientInnerAuth(newBR, newBW, subType, cfg)

	case 260, 261, 262: // X509 sub-types: verify server certificate
		tlsCfg := cfg.TLSConfig
		if tlsCfg == nil {
			tlsCfg = &tls.Config{}
		}
		upgConn, newBR, newBW, err := upgradeTLSClient(conn, br, bw, tlsCfg)
		if err != nil {
			return conn, br, bw, err
		}
		return upgConn, newBR, newBW, performClientInnerAuth(newBR, newBW, subType, cfg)

	default:
		return conn, br, bw, fmt.Errorf("vencrypt client: unsupported sub-type %d", subType)
	}
}

func performClientInnerAuth(br *bufio.Reader, bw *bufio.Writer, subType uint32, cfg VeNCryptClientConfig) error {
	switch subType {
	case 257, 260: // TLSNone, X509None — no inner auth, server still sends SecurityResult
		return readVeNCryptSecurityResult(br)

	case 258, 261: // TLSVNCAuth, X509VNCAuth
		if err := VNCAuthClient(struct {
			*bufio.Reader
			*bufio.Writer
		}{br, bw}, cfg.VNCPassword); err != nil {
			return err
		}
		// Flush the encrypted response before reading SecurityResult.
		if err := bw.Flush(); err != nil {
			return fmt.Errorf("vencrypt: flush vnc auth response: %w", err)
		}
		return readVeNCryptSecurityResult(br)

	case 259, 262: // TLSPlain, X509Plain
		if err := sendPlainAuth(bw, cfg.Username, cfg.Password); err != nil {
			return err
		}
		if err := bw.Flush(); err != nil {
			return fmt.Errorf("vencrypt: flush plain auth: %w", err)
		}
		return readVeNCryptSecurityResult(br)

	default:
		return fmt.Errorf("vencrypt client: unknown inner sub-type %d", subType)
	}
}

// readVeNCryptSecurityResult reads the 4-byte SecurityResult from the server.
// If the result indicates failure it also reads the optional reason string.
func readVeNCryptSecurityResult(br *bufio.Reader) error {
	var result uint32
	if err := binary.Read(br, binary.BigEndian, &result); err != nil {
		return fmt.Errorf("vencrypt: read security result: %w", err)
	}
	if result != 0 {
		// Try to read the optional reason string (uint32 length + bytes).
		var reasonLen uint32
		if err := binary.Read(br, binary.BigEndian, &reasonLen); err != nil {
			return fmt.Errorf("vencrypt: authentication failed")
		}
		if reasonLen > 0 && reasonLen < 1024 {
			reason := make([]byte, reasonLen)
			_, _ = io.ReadFull(br, reason)
			return fmt.Errorf("vencrypt: authentication failed: %s", reason)
		}
		return fmt.Errorf("vencrypt: authentication failed")
	}
	return nil
}

// sendPlainAuth writes the Plain auth payload (username+password) to the server.
func sendPlainAuth(bw *bufio.Writer, username, password string) error {
	ulen := uint16(len(username))
	if err := binary.Write(bw, binary.BigEndian, ulen); err != nil {
		return fmt.Errorf("vencrypt plain client: write username len: %w", err)
	}
	if _, err := bw.WriteString(username); err != nil {
		return fmt.Errorf("vencrypt plain client: write username: %w", err)
	}
	plen := uint16(len(password))
	if err := binary.Write(bw, binary.BigEndian, plen); err != nil {
		return fmt.Errorf("vencrypt plain client: write password len: %w", err)
	}
	if _, err := bw.WriteString(password); err != nil {
		return fmt.Errorf("vencrypt plain client: write password: %w", err)
	}
	return nil
}

// clientTLSConfigInsecure returns a TLS config that skips certificate verification
// (used for TLS sub-types 257–259 where anonymous TLS is expected).
// Never mutates the provided base config.
func clientTLSConfigInsecure(base *tls.Config) *tls.Config {
	var cfg *tls.Config
	if base != nil {
		cfg = base.Clone()
	} else {
		cfg = &tls.Config{}
	}
	cfg.InsecureSkipVerify = true //nolint:gosec // VeNCrypt TLS sub-types intentionally skip verification
	return cfg
}

// chooseVeNCryptSubType returns the first entry in preferred that is also
// present in serverTypes. Returns 0 if no match is found.
func chooseVeNCryptSubType(serverTypes, preferred []uint32) uint32 {
	serverSet := make(map[uint32]bool, len(serverTypes))
	for _, st := range serverTypes {
		serverSet[st] = true
	}
	for _, p := range preferred {
		if serverSet[p] {
			return p
		}
	}
	return 0
}
