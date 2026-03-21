package wsproxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rixcian/redvnc/rfb"
	"nhooyr.io/websocket"
)

// checkPortAvailable checks whether the given address is available for listening.
// It dials the address with a short timeout; if a connection is established the
// port is already occupied and an error is returned. Works on all platforms
// (Linux, macOS, Windows) without any OS-specific code.
func checkPortAvailable(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid address %q: %w", addr, err)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	checkAddr := net.JoinHostPort(host, port)
	conn, err := net.DialTimeout("tcp", checkAddr, time.Second)
	if err != nil {
		// Could not connect — nothing is listening on that port.
		return nil
	}
	conn.Close()
	return fmt.Errorf("port %s is already in use by another process", addr)
}

// Config holds the proxy server configuration.
type Config struct {
	// ListenAddr is the HTTP/WebSocket listen address (e.g. ":8080").
	ListenAddr string

	// AllowedVNCTargets is a whitelist of VNC server addresses (host:port).
	// If empty, clients may connect to ANY target (open relay).
	AllowedVNCTargets []string

	// DefaultVNCPassword is the VNC authentication password used when the
	// client does not supply one.
	DefaultVNCPassword string

	// MaxConnections is the maximum number of simultaneous client sessions. Default: 100.
	MaxConnections int

	// MaxConnectionsPerTarget limits sessions to the same VNC target. Default: 10.
	MaxConnectionsPerTarget int

	// DefaultUploadDir is the fallback directory for file uploads.
	DefaultUploadDir string

	// AllowedUploadDirs restricts which directories clients may upload into.
	AllowedUploadDirs []string

	// MaxUploadSize is the maximum file upload size in bytes. Default: 100MB.
	MaxUploadSize int64

	// TLSCertFile and TLSKeyFile enable TLS.
	TLSCertFile string
	TLSKeyFile  string

	// AllowedOrigins restricts WebSocket connections by Origin header.
	AllowedOrigins []string

	// RateLimit configures authentication rate limiting.
	// If zero value, DefaultRateLimitConfig() is used.
	RateLimit RateLimitConfig

	// ShutdownTimeout is the maximum time to wait for sessions to drain during shutdown.
	// Default: 30s.
	ShutdownTimeout time.Duration

	// Logger is the structured logger. If nil, slog.Default() is used.
	Logger *slog.Logger
}

// Server is the WebSocket VNC proxy server.
type Server struct {
	config  Config
	connMgr *ConnectionManager
	httpSrv *http.Server

	allowedTargets map[string]bool
	allowedOrigins map[string]bool
	allowedUpDirs  []string
	rateLimiter    *RateLimiter
	logger         *slog.Logger

	draining     bool
	drainingMu   sync.RWMutex
	shutdownOnce sync.Once
}

// NewServer creates a new proxy server with the given configuration.
func NewServer(config Config) *Server {
	if config.MaxConnections <= 0 {
		config.MaxConnections = 100
	}
	if config.MaxConnectionsPerTarget <= 0 {
		config.MaxConnectionsPerTarget = 10
	}
	if config.MaxUploadSize <= 0 {
		config.MaxUploadSize = 100 * 1024 * 1024 // 100MB
	}
	if config.DefaultUploadDir == "" {
		config.DefaultUploadDir = defaultDownloadsDir()
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = 30 * time.Second
	}

	// Build allowed targets map
	allowedTargets := make(map[string]bool)
	for _, t := range config.AllowedVNCTargets {
		allowedTargets[t] = true
	}

	// Build allowed origins map
	allowedOrigins := make(map[string]bool)
	for _, o := range config.AllowedOrigins {
		allowedOrigins[o] = true
	}

	// Initialize rate limit config
	if config.RateLimit.MaxAttempts == 0 {
		config.RateLimit = DefaultRateLimitConfig()
	}

	// Resolve allowed upload directories
	allowedUpDirs := make([]string, 0, len(config.AllowedUploadDirs))
	for _, d := range config.AllowedUploadDirs {
		abs, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		allowedUpDirs = append(allowedUpDirs, abs)
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{
		config:         config,
		connMgr:        NewConnectionManager(config.MaxConnections, config.MaxConnectionsPerTarget),
		allowedTargets: allowedTargets,
		allowedOrigins: allowedOrigins,
		allowedUpDirs:  allowedUpDirs,
		rateLimiter:    NewRateLimiter(config.RateLimit),
		logger:         logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ready", s.handleReady)

	s.httpSrv = &http.Server{
		Addr:    config.ListenAddr,
		Handler: mux,
	}

	return s
}

// ListenAndServe starts the proxy server.
func (s *Server) ListenAndServe() error {
	// Set up graceful shutdown on signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		s.logger.Info("shutting down")
		s.Shutdown()
	}()

	if err := checkPortAvailable(s.config.ListenAddr); err != nil {
		return err
	}

	s.logger.Info("wsproxy listening", "addr", s.config.ListenAddr)

	if s.config.TLSCertFile != "" && s.config.TLSKeyFile != "" {
		s.httpSrv.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
		return s.httpSrv.ListenAndServeTLS(s.config.TLSCertFile, s.config.TLSKeyFile)
	}
	return s.httpSrv.ListenAndServe()
}

// Shutdown gracefully shuts down the server, draining active sessions.
func (s *Server) Shutdown() {
	s.shutdownOnce.Do(func() {
		s.logger.Info("shutdown: setting server to draining state")

		// 1. Mark as draining — health returns 503, new WS connections rejected
		s.drainingMu.Lock()
		s.draining = true
		s.drainingMu.Unlock()

		// 2. Stop the rate limiter background goroutine
		s.rateLimiter.Stop()

		// 3. Send close frame to all active WebSocket sessions
		sessions := s.connMgr.All()
		s.logger.Info("shutdown: closing active sessions", "count", len(sessions))
		for _, sess := range sessions {
			sess.WSConn.Close(websocket.StatusGoingAway, "server shutting down")
		}

		// 4. Wait for sessions to drain within the shutdown timeout
		s.logger.Info("shutdown: waiting for sessions to drain", "timeout", s.config.ShutdownTimeout)
		ctx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
		defer cancel()

		// 5. Shut down the HTTP server (force-closes remaining connections after timeout)
		if err := s.httpSrv.Shutdown(ctx); err != nil {
			s.logger.Warn("shutdown: HTTP server shutdown error", "error", err)
		}

		s.logger.Info("shutdown: complete")
	})
}

// isDraining returns true if the server is shutting down.
func (s *Server) isDraining() bool {
	s.drainingMu.RLock()
	defer s.drainingMu.RUnlock()
	return s.draining
}

// handleHealth returns HTTP 200 if the server is running.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": rfb.Version})
}

// handleReady returns HTTP 200 if the server is accepting connections, 503 if draining or at capacity.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.isDraining() {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":          "not ready",
			"reason":          "shutting down",
			"active_sessions": s.connMgr.Count(),
		})
		return
	}

	if s.connMgr.Count() >= s.config.MaxConnections {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":          "not ready",
			"reason":          "at capacity",
			"active_sessions": s.connMgr.Count(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":          "ready",
		"active_sessions": s.connMgr.Count(),
	})
}

// extractIP extracts the IP address from a remote address string (host:port).
func extractIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Reject new connections if draining
	if s.isDraining() {
		http.Error(w, `{"error": "server is shutting down"}`, http.StatusServiceUnavailable)
		return
	}

	// Check rate limiter
	clientIP := extractIP(r.RemoteAddr)
	if !s.rateLimiter.IsAllowed(clientIP) {
		w.Header().Set("Retry-After", "30")
		http.Error(w, `{"error": "too many failed attempts, try again later"}`, http.StatusTooManyRequests)
		return
	}

	// Validate origin
	if len(s.allowedOrigins) > 0 {
		origin := r.Header.Get("Origin")
		if origin != "" && !s.allowedOrigins[origin] {
			http.Error(w, `{"error": "origin not allowed"}`, http.StatusForbidden)
			return
		}
	}

	// Parse target
	target := r.URL.Query().Get("target")
	if target == "" {
		http.Error(w, `{"error": "missing target parameter"}`, http.StatusBadRequest)
		return
	}

	// Validate target format (host:port)
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" || port == "" {
		http.Error(w, `{"error": "invalid target format, expected host:port"}`, http.StatusBadRequest)
		return
	}

	// Check allowlist
	if len(s.allowedTargets) > 0 && !s.allowedTargets[target] {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "target not allowed"})
		return
	}

	// Parse optional password
	password := r.URL.Query().Get("password")
	if password == "" {
		password = s.config.DefaultVNCPassword
	}

	// Accept WebSocket upgrade
	wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:   []string{"binary"},
		OriginPatterns: s.originPatterns(),
	})
	if err != nil {
		s.logger.Error("websocket accept error", "error", err)
		return
	}

	// Create session
	sess := NewSession(r.RemoteAddr, target, password, wsConn)

	// Check connection limits
	if !s.connMgr.TryAdd(sess) {
		wsConn.Close(websocket.StatusTryAgainLater, "connection limit exceeded")
		return
	}

	s.logger.Info("session started", "session_id", sess.ID, "remote_addr", sess.ClientAddr, "target", sess.Target, "user_agent", r.UserAgent())

	// Run the proxy relay (blocks until session ends)
	proxy := NewProxy(sess, s)
	proxy.Run(r.Context())

	// Cleanup
	sess.CleanupUploads()
	s.connMgr.Remove(sess)
	s.logger.Info("session ended", "session_id", sess.ID)
}

func (s *Server) originPatterns() []string {
	if len(s.allowedOrigins) == 0 {
		return []string{"*"}
	}
	patterns := make([]string, 0, len(s.allowedOrigins))
	for o := range s.allowedOrigins {
		patterns = append(patterns, o)
	}
	return patterns
}

// IsUploadDirAllowed checks if the given directory is authorized for uploads.
func (s *Server) IsUploadDirAllowed(dir string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}

	// Resolve symlinks
	resolved, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		// Directory might not exist yet, check parent
		resolved = absDir
	}

	// If no allowed dirs configured, only allow default
	if len(s.allowedUpDirs) == 0 {
		defaultAbs, _ := filepath.Abs(s.config.DefaultUploadDir)
		return isSubdirOf(resolved, defaultAbs)
	}

	for _, allowed := range s.allowedUpDirs {
		if isSubdirOf(resolved, allowed) {
			return true
		}
	}
	return false
}

// isSubdirOf checks if child is equal to or a subdirectory of parent.
func isSubdirOf(child, parent string) bool {
	child = filepath.Clean(child)
	parent = filepath.Clean(parent)
	if child == parent {
		return true
	}
	return strings.HasPrefix(child, parent+string(filepath.Separator))
}

// defaultDownloadsDir returns the OS-specific Downloads directory.
func defaultDownloadsDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "linux":
		if xdgDir := os.Getenv("XDG_DOWNLOAD_DIR"); xdgDir != "" {
			return xdgDir
		}
		return filepath.Join(home, "Downloads")
	case "darwin":
		return filepath.Join(home, "Downloads")
	case "windows":
		if profile := os.Getenv("USERPROFILE"); profile != "" {
			return filepath.Join(profile, "Downloads")
		}
		return filepath.Join(home, "Downloads")
	default:
		return filepath.Join(home, "Downloads")
	}
}

// SanitizeFilename removes dangerous characters from a filename.
func SanitizeFilename(name string) (string, error) {
	// Use only the basename
	name = filepath.Base(name)

	if name == "." || name == ".." || name == "" {
		return "", fmt.Errorf("invalid filename")
	}

	// Remove null bytes and control characters
	var cleaned strings.Builder
	for _, r := range name {
		if r == 0 || r < 32 {
			continue
		}
		// Remove path separators
		if r == '/' || r == '\\' {
			continue
		}
		cleaned.WriteRune(r)
	}

	result := cleaned.String()
	if result == "" || result == "." || result == ".." {
		return "", fmt.Errorf("invalid filename after sanitization")
	}

	return result, nil
}

// UniqueFilePath returns a path that doesn't conflict with existing files.
// If file.txt exists, it tries file(1).txt, file(2).txt, etc.
func UniqueFilePath(dir, name string) string {
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}

	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)

	for i := 1; i < 10000; i++ {
		path = filepath.Join(dir, fmt.Sprintf("%s(%d)%s", base, i, ext))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path
		}
	}
	return path
}
