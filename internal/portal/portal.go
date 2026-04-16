//go:build linux

// Package portal provides a Go wrapper around the xdg-desktop-portal
// interfaces used by RedVNC on Wayland sessions:
//
//   - org.freedesktop.portal.ScreenCast (screen capture)
//   - org.freedesktop.portal.RemoteDesktop (keyboard + pointer input)
//
// The portal API is asynchronous: every method call returns a
// "request handle" object, and the real result is delivered via a
// Response signal on that handle. This package hides that plumbing
// behind synchronous Go methods.
//
// A Session bundles ScreenCast and RemoteDesktop together so the user
// only sees a single consent dialog. After a successful Start() the
// caller can read PipeWireNodeID() to connect a PipeWire client for
// screen capture, and use the Notify* methods to inject input.
//
// References:
//
//   - https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.ScreenCast.html
//   - https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.RemoteDesktop.html
package portal

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	busName    = "org.freedesktop.portal.Desktop"
	objectPath = "/org/freedesktop/portal/desktop"

	ifaceScreenCast    = "org.freedesktop.portal.ScreenCast"
	ifaceRemoteDesktop = "org.freedesktop.portal.RemoteDesktop"
	ifaceRequest       = "org.freedesktop.portal.Request"
	ifaceSession       = "org.freedesktop.portal.Session"
)

// Source type constants for ScreenCast.SelectSources.
const (
	SourceTypeMonitor uint32 = 1
	SourceTypeWindow  uint32 = 2
	SourceTypeVirtual uint32 = 4
)

// Cursor mode constants for ScreenCast.SelectSources.
const (
	CursorModeHidden   uint32 = 1
	CursorModeEmbedded uint32 = 2
	CursorModeMetadata uint32 = 4
)

// Device type flags for RemoteDesktop.SelectDevices.
const (
	DeviceKeyboard uint32 = 1
	DevicePointer  uint32 = 2
	DeviceTouch    uint32 = 4
)

// PersistMode controls whether the portal remembers the user's choice
// between runs of the application.
const (
	PersistModeNone         uint32 = 0
	PersistModeApplication  uint32 = 1
	PersistModeUntilRevoked uint32 = 2
)

// SessionOpts configures a new Session.
type SessionOpts struct {
	// Capture, when true, enables ScreenCast source selection and
	// PipeWire stream delivery.
	Capture bool

	// Input, when true, enables RemoteDesktop device selection and
	// allows the Notify* methods to inject keyboard and pointer events.
	Input bool

	// SourceTypes is a bitmask of SourceType* constants for capture.
	// Defaults to SourceTypeMonitor when zero.
	SourceTypes uint32

	// CursorMode controls how the cursor is included in captured frames.
	// Defaults to CursorModeEmbedded when zero.
	CursorMode uint32

	// Devices is a bitmask of Device* constants for input.
	// Defaults to DeviceKeyboard|DevicePointer when zero.
	Devices uint32

	// RestoreTokenPath, when non-empty, is a file path where the
	// session's restore token is persisted. If the file exists on
	// session creation the token is passed to SelectSources so the
	// user is not re-prompted. Leave empty to disable restoration.
	RestoreTokenPath string

	// StartTimeout bounds how long Start() will wait for the user
	// consent dialog to complete. Defaults to 2 minutes.
	StartTimeout time.Duration
}

// Session is an authenticated xdg-desktop-portal session with an
// associated ScreenCast or RemoteDesktop grant (or both).
type Session struct {
	opts SessionOpts

	conn          *dbus.Conn
	senderToken   string // sender name with '.' → '_' for request paths
	sessionHandle dbus.ObjectPath

	// Signal-matched response channels, keyed by request path.
	respMu sync.Mutex
	resps  map[dbus.ObjectPath]chan responseSignal

	// Populated after Start().
	streams      []Stream
	restoreToken string

	closed  bool
	closeMu sync.Mutex
}

// Stream describes a PipeWire video stream returned by ScreenCast.Start.
type Stream struct {
	NodeID uint32
	Width  int32
	Height int32
}

type responseSignal struct {
	code    uint32
	results map[string]dbus.Variant
}

// New creates and authenticates a portal session according to opts.
//
// For combined capture+input sessions the RemoteDesktop interface is
// used as the session owner (this is required by the portal spec).
// The user will see a single consent dialog that covers both.
//
// On success the session is active and the caller should call Close()
// when done. Start() must have been completed for PipeWireStreams() to
// return populated data.
func New(opts SessionOpts) (*Session, error) {
	if !opts.Capture && !opts.Input {
		return nil, errors.New("portal session requires at least one of Capture or Input")
	}
	if opts.SourceTypes == 0 {
		opts.SourceTypes = SourceTypeMonitor
	}
	if opts.CursorMode == 0 {
		opts.CursorMode = CursorModeEmbedded
	}
	if opts.Devices == 0 {
		opts.Devices = DeviceKeyboard | DevicePointer
	}
	if opts.StartTimeout == 0 {
		opts.StartTimeout = 2 * time.Minute
	}

	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("portal: connect to session bus: %w", err)
	}

	s := &Session{
		opts:        opts,
		conn:        conn,
		senderToken: senderToken(conn.Names()[0]),
		resps:       make(map[dbus.ObjectPath]chan responseSignal),
	}

	if err := s.installResponseListener(); err != nil {
		return nil, err
	}

	if err := s.createSession(); err != nil {
		s.Close()
		return nil, err
	}
	if opts.Input {
		if err := s.selectDevices(); err != nil {
			s.Close()
			return nil, err
		}
	}
	if opts.Capture {
		if err := s.selectSources(); err != nil {
			s.Close()
			return nil, err
		}
	}
	if err := s.start(); err != nil {
		s.Close()
		return nil, err
	}

	return s, nil
}

// PipeWireStreams returns the PipeWire video streams associated with
// this session. Empty for input-only sessions.
func (s *Session) PipeWireStreams() []Stream {
	return s.streams
}

// PipeWireNodeID returns the primary PipeWire node ID for the first
// stream. Returns (0, false) if no streams are available.
func (s *Session) PipeWireNodeID() (uint32, bool) {
	if len(s.streams) == 0 {
		return 0, false
	}
	return s.streams[0].NodeID, true
}

// OpenPipeWireRemote asks the portal for an authenticated file
// descriptor suitable for connecting a PipeWire client to the same
// remote the streams are exposed on. Returns the raw fd which the
// caller must eventually close.
func (s *Session) OpenPipeWireRemote() (int, error) {
	obj := s.conn.Object(busName, objectPath)
	var fd dbus.UnixFD
	err := obj.Call(
		ifaceScreenCast+".OpenPipeWireRemote",
		0,
		s.sessionHandle,
		map[string]dbus.Variant{},
	).Store(&fd)
	if err != nil {
		return -1, fmt.Errorf("portal: OpenPipeWireRemote: %w", err)
	}
	return int(fd), nil
}

// Conn returns the underlying D-Bus connection. It is used by the
// RemoteDesktop input methods in input.go.
func (s *Session) Conn() *dbus.Conn {
	return s.conn
}

// SessionHandle returns the portal session object path.
func (s *Session) SessionHandle() dbus.ObjectPath {
	return s.sessionHandle
}

// Close tears down the portal session.
func (s *Session) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	if s.sessionHandle != "" {
		// Best-effort close. We intentionally ignore the error because
		// the session may already be gone if Start() failed.
		obj := s.conn.Object(busName, s.sessionHandle)
		_ = obj.Call(ifaceSession+".Close", 0).Err
	}
	// Leave the shared session bus open — it is owned by godbus.
	return nil
}

// --- internal plumbing ---------------------------------------------------

// senderToken derives the per-sender token that goes into request and
// session object paths. It is the unique D-Bus name with ":" stripped
// and "." replaced with "_". Example: ":1.42" → "1_42".
func senderToken(name string) string {
	name = strings.TrimPrefix(name, ":")
	return strings.ReplaceAll(name, ".", "_")
}

// randomToken returns a short hex token used to uniquify request and
// session handles. The portal builds the expected path from this token,
// so callers can precompute the path and install a signal match before
// making the D-Bus call.
func randomToken(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// requestPath returns the object path the portal will use for a Request
// corresponding to the given handle_token option.
func (s *Session) requestPath(token string) dbus.ObjectPath {
	return dbus.ObjectPath(fmt.Sprintf(
		"/org/freedesktop/portal/desktop/request/%s/%s",
		s.senderToken, token,
	))
}

// sessionPath returns the object path the portal will use for a Session
// corresponding to the given session_handle_token option.
func (s *Session) sessionPath(token string) dbus.ObjectPath {
	return dbus.ObjectPath(fmt.Sprintf(
		"/org/freedesktop/portal/desktop/session/%s/%s",
		s.senderToken, token,
	))
}

// installResponseListener adds a match rule for org.freedesktop.portal.Request
// Response signals and starts a goroutine that demultiplexes them to
// per-path channels registered via expectResponse.
func (s *Session) installResponseListener() error {
	err := s.conn.AddMatchSignal(
		dbus.WithMatchInterface(ifaceRequest),
		dbus.WithMatchMember("Response"),
	)
	if err != nil {
		return fmt.Errorf("portal: AddMatchSignal: %w", err)
	}
	ch := make(chan *dbus.Signal, 8)
	s.conn.Signal(ch)
	go func() {
		for sig := range ch {
			if sig.Name != ifaceRequest+".Response" || len(sig.Body) != 2 {
				continue
			}
			code, _ := sig.Body[0].(uint32)
			results, _ := sig.Body[1].(map[string]dbus.Variant)

			s.respMu.Lock()
			respCh, ok := s.resps[sig.Path]
			if ok {
				delete(s.resps, sig.Path)
			}
			s.respMu.Unlock()
			if ok {
				respCh <- responseSignal{code: code, results: results}
			}
		}
	}()
	return nil
}

// expectResponse registers a channel for the response signal delivered
// to the given request path. The returned function releases the
// registration if the caller bails out before the signal arrives.
func (s *Session) expectResponse(path dbus.ObjectPath) (<-chan responseSignal, func()) {
	ch := make(chan responseSignal, 1)
	s.respMu.Lock()
	s.resps[path] = ch
	s.respMu.Unlock()
	cleanup := func() {
		s.respMu.Lock()
		delete(s.resps, path)
		s.respMu.Unlock()
	}
	return ch, cleanup
}

// portalCall makes a portal method call that returns a request handle
// and waits for the matching Response signal, returning the results
// dictionary. If the response code is non-zero the call is treated as
// a failure (the user cancelled, or the portal rejected the request).
func (s *Session) portalCall(
	iface, method string,
	timeout time.Duration,
	handleToken string,
	args ...interface{},
) (map[string]dbus.Variant, error) {
	expected := s.requestPath(handleToken)
	respCh, cleanup := s.expectResponse(expected)
	defer cleanup()

	obj := s.conn.Object(busName, objectPath)
	var actual dbus.ObjectPath
	if err := obj.Call(iface+"."+method, 0, args...).Store(&actual); err != nil {
		return nil, fmt.Errorf("portal: %s: %w", method, err)
	}
	// The actual path should match what we computed. If it doesn't
	// (some portal backends differ on escaping), re-register under the
	// actual path so the response is still routed.
	if actual != expected {
		s.respMu.Lock()
		delete(s.resps, expected)
		s.resps[actual] = make(chan responseSignal, 1)
		ch := s.resps[actual]
		s.respMu.Unlock()
		respCh = ch
	}

	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	select {
	case resp := <-respCh:
		if resp.code != 0 {
			return nil, fmt.Errorf("portal: %s response code %d", method, resp.code)
		}
		return resp.results, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("portal: %s timed out after %s", method, timeout)
	}
}

func (s *Session) createSession() error {
	handleToken := randomToken("redvnc_req_")
	sessionToken := randomToken("redvnc_sess_")

	iface := ifaceScreenCast
	if s.opts.Input {
		// RemoteDesktop owns combined sessions.
		iface = ifaceRemoteDesktop
	}

	opts := map[string]dbus.Variant{
		"handle_token":         dbus.MakeVariant(handleToken),
		"session_handle_token": dbus.MakeVariant(sessionToken),
	}
	results, err := s.portalCall(iface, "CreateSession", 30*time.Second, handleToken, opts)
	if err != nil {
		return err
	}
	handle, ok := results["session_handle"].Value().(string)
	if !ok || handle == "" {
		// Some backends omit this field and rely on the deterministic
		// path. Use the computed one.
		s.sessionHandle = s.sessionPath(sessionToken)
	} else {
		s.sessionHandle = dbus.ObjectPath(handle)
	}
	return nil
}

func (s *Session) selectDevices() error {
	handleToken := randomToken("redvnc_req_")
	opts := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(handleToken),
		"types":        dbus.MakeVariant(s.opts.Devices),
	}
	_, err := s.portalCall(
		ifaceRemoteDesktop, "SelectDevices",
		30*time.Second, handleToken,
		s.sessionHandle, opts,
	)
	return err
}

func (s *Session) selectSources() error {
	handleToken := randomToken("redvnc_req_")
	opts := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(handleToken),
		"types":        dbus.MakeVariant(s.opts.SourceTypes),
		"multiple":     dbus.MakeVariant(false),
		"cursor_mode":  dbus.MakeVariant(s.opts.CursorMode),
		"persist_mode": dbus.MakeVariant(PersistModeUntilRevoked),
	}
	if token := s.loadRestoreToken(); token != "" {
		opts["restore_token"] = dbus.MakeVariant(token)
	}
	_, err := s.portalCall(
		ifaceScreenCast, "SelectSources",
		30*time.Second, handleToken,
		s.sessionHandle, opts,
	)
	return err
}

// rawStream is the D-Bus wire struct for a ScreenCast stream entry:
// (u, a{sv}) — node_id plus a properties dict.
type rawStream = struct {
	NodeID uint32
	Props  map[string]dbus.Variant
}

func (s *Session) start() error {
	handleToken := randomToken("redvnc_req_")
	opts := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(handleToken),
	}
	iface := ifaceScreenCast
	if s.opts.Input {
		iface = ifaceRemoteDesktop
	}
	results, err := s.portalCall(
		iface, "Start",
		s.opts.StartTimeout, handleToken,
		s.sessionHandle, "", opts,
	)
	if err != nil {
		return err
	}
	if rt, ok := results["restore_token"].Value().(string); ok && rt != "" {
		s.restoreToken = rt
		s.saveRestoreToken(rt)
	}
	if raw, ok := results["streams"]; ok {
		var streams []rawStream
		if err := raw.Store(&streams); err == nil {
			for _, st := range streams {
				stream := Stream{NodeID: st.NodeID}
				if sz, ok := st.Props["size"]; ok {
					var wh [2]int32
					_ = sz.Store(&wh)
					stream.Width = wh[0]
					stream.Height = wh[1]
				}
				s.streams = append(s.streams, stream)
			}
		}
	}
	return nil
}

func (s *Session) loadRestoreToken() string {
	if s.opts.RestoreTokenPath == "" {
		return ""
	}
	data, err := os.ReadFile(s.opts.RestoreTokenPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (s *Session) saveRestoreToken(token string) {
	if s.opts.RestoreTokenPath == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.opts.RestoreTokenPath), 0o700)
	_ = os.WriteFile(s.opts.RestoreTokenPath, []byte(token), 0o600)
}
