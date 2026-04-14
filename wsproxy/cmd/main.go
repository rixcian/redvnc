// Command redvnc-wsproxy runs the WebSocket-to-TCP VNC proxy server.
package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/rixcian/redvnc/wsproxy"
)

// version is set at link time via -ldflags "-X main.version=...".
var version = "dev"

type stringSlice []string

func (s *stringSlice) String() string { return fmt.Sprintf("%v", *s) }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	var (
		configFile     = flag.String("config", "", "Path to JSON configuration file")
		listen         = flag.String("listen", "", "HTTP/WebSocket listen address")
		tlsCert        = flag.String("tls-cert", "", "TLS certificate file")
		tlsKey         = flag.String("tls-key", "", "TLS key file")
		requireTLS     = flag.Bool("require-tls", false, "Refuse to start without TLS configuration")
		password       = flag.String("vnc-password", "", "Default VNC password")
		passwordFile   = flag.String("vnc-password-file", "", "File containing the default VNC password (first line)")
		allowAnyTarget = flag.Bool("allow-any-target", false, "Explicitly allow connections to any VNC server")
		maxConns       = flag.Int("max-connections", 0, "Maximum simultaneous connections")
		maxPerTgt      = flag.Int("max-connections-per-target", 0, "Maximum connections per VNC target")
		defaultDir     = flag.String("default-upload-dir", "", "Default upload directory")
		maxUpload      = flag.Int64("max-upload-size", 0, "Maximum upload file size in bytes")
		logFormat      = flag.String("log-format", "", "Log format: text or json")
		logLevel       = flag.String("log-level", "", "Log level: debug, info, warn, error")
		shutdownTimeout = flag.Duration("shutdown-timeout", 0, "Maximum time to wait for sessions to drain during shutdown")
		showVersion     = flag.Bool("version", false, "Print version and exit")

		allowedTargets stringSlice
		allowedOrigins stringSlice
		allowedUpDirs  stringSlice
	)

	flag.Var(&allowedTargets, "allowed-vnc-target", "Allowed VNC target host:port (repeatable)")
	flag.Var(&allowedOrigins, "allowed-origin", "Allowed WebSocket origin (repeatable)")
	flag.Var(&allowedUpDirs, "allowed-upload-dir", "Allowed upload directory (repeatable)")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	// Track which flags were explicitly set
	flagSet := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { flagSet[f.Name] = true })

	// Step 1: Load config file (if provided)
	var fileCfg *wsproxy.FileConfig
	if *configFile != "" {
		var err error
		fileCfg, err = wsproxy.LoadFileConfig(*configFile)
		if err != nil {
			log.Fatalf("error: %v", err)
		}

		// Apply env var overrides on top of file config
		wsproxy.ApplyEnvOverrides(fileCfg)

		// Validate
		if errs := wsproxy.ValidateFileConfig(fileCfg); len(errs) > 0 {
			for _, e := range errs {
				log.Printf("config error: %s", e)
			}
			os.Exit(1)
		}
	}

	// Step 2: Build final values with priority: CLI flags > env vars/config file > defaults
	finalListen := ":8080"
	if fileCfg != nil && fileCfg.Listen != "" {
		finalListen = fileCfg.Listen
	}
	if flagSet["listen"] {
		finalListen = *listen
	}

	finalTLSCert := ""
	finalTLSKey := ""
	finalRequireTLS := false
	if fileCfg != nil && fileCfg.TLS != nil {
		finalTLSCert = fileCfg.TLS.Cert
		finalTLSKey = fileCfg.TLS.Key
		finalRequireTLS = fileCfg.TLS.Require
	}
	if flagSet["tls-cert"] {
		finalTLSCert = *tlsCert
	}
	if flagSet["tls-key"] {
		finalTLSKey = *tlsKey
	}
	if flagSet["require-tls"] {
		finalRequireTLS = *requireTLS
	}

	// Resolve VNC password: CLI flag > password file > config file > env var
	vncPassword := ""
	if fileCfg != nil && fileCfg.Security != nil {
		vncPassword = fileCfg.Security.VNCPassword
		if vncPassword == "" && fileCfg.Security.VNCPasswordFile != "" {
			data, err := os.ReadFile(fileCfg.Security.VNCPasswordFile)
			if err != nil {
				log.Fatalf("error: cannot read password file from config: %v", err)
			}
			vncPassword = strings.SplitN(string(data), "\n", 2)[0]
			vncPassword = strings.TrimRight(vncPassword, "\r")
		}
	}
	if flagSet["vnc-password-file"] {
		data, err := os.ReadFile(*passwordFile)
		if err != nil {
			log.Fatalf("error: cannot read password file: %v", err)
		}
		vncPassword = strings.SplitN(string(data), "\n", 2)[0]
		vncPassword = strings.TrimRight(vncPassword, "\r")
	}
	if flagSet["vnc-password"] {
		vncPassword = *password
	}
	// Env var fallback (only if nothing else set it)
	if vncPassword == "" {
		if envPass := os.Getenv("REDVNC_VNC_PASSWORD"); envPass != "" {
			vncPassword = envPass
		}
	}

	// Allowed targets
	finalAllowAnyTarget := false
	finalTargets := []string(allowedTargets)
	if fileCfg != nil && fileCfg.Security != nil {
		if fileCfg.Security.AllowAnyTarget {
			finalAllowAnyTarget = true
		}
		if len(finalTargets) == 0 {
			finalTargets = fileCfg.Security.AllowedVNCTargets
		}
	}
	if flagSet["allow-any-target"] {
		finalAllowAnyTarget = *allowAnyTarget
	}

	// Require explicit opt-in for open relay
	if len(finalTargets) == 0 && !finalAllowAnyTarget {
		log.Fatal("error: --allowed-vnc-target is required (use --allow-any-target to explicitly allow connections to any VNC server)")
	}

	// Require TLS if configured
	if finalRequireTLS && (finalTLSCert == "" || finalTLSKey == "") {
		log.Fatal("error: TLS is required but --tls-cert and --tls-key are not configured")
	}

	// Allowed origins
	finalOrigins := []string(allowedOrigins)
	if len(finalOrigins) == 0 && fileCfg != nil && fileCfg.Security != nil {
		finalOrigins = fileCfg.Security.AllowedOrigins
	}

	// Connection limits
	finalMaxConns := 100
	finalMaxPerTgt := 10
	if fileCfg != nil && fileCfg.Limits != nil {
		if fileCfg.Limits.MaxConnections > 0 {
			finalMaxConns = fileCfg.Limits.MaxConnections
		}
		if fileCfg.Limits.MaxConnectionsPerTarget > 0 {
			finalMaxPerTgt = fileCfg.Limits.MaxConnectionsPerTarget
		}
	}
	if flagSet["max-connections"] {
		finalMaxConns = *maxConns
	}
	if flagSet["max-connections-per-target"] {
		finalMaxPerTgt = *maxPerTgt
	}

	// Upload settings
	finalDefaultDir := ""
	finalUpDirs := []string(allowedUpDirs)
	var finalMaxUpload int64 = 100 * 1024 * 1024
	if fileCfg != nil && fileCfg.Uploads != nil {
		finalDefaultDir = fileCfg.Uploads.DefaultDir
		if len(finalUpDirs) == 0 {
			finalUpDirs = fileCfg.Uploads.AllowedDirs
		}
	}
	if fileCfg != nil && fileCfg.Limits != nil && fileCfg.Limits.MaxUploadSize != "" {
		if size, err := wsproxy.ParseByteSize(fileCfg.Limits.MaxUploadSize); err == nil {
			finalMaxUpload = size
		}
	}
	if flagSet["default-upload-dir"] {
		finalDefaultDir = *defaultDir
	}
	if flagSet["max-upload-size"] {
		finalMaxUpload = *maxUpload
	}

	// Logging
	finalLogLevel := "info"
	finalLogFormat := "text"
	if fileCfg != nil && fileCfg.Logging != nil {
		if fileCfg.Logging.Level != "" {
			finalLogLevel = fileCfg.Logging.Level
		}
		if fileCfg.Logging.Format != "" {
			finalLogFormat = fileCfg.Logging.Format
		}
	}
	if flagSet["log-level"] {
		finalLogLevel = *logLevel
	}
	if flagSet["log-format"] {
		finalLogFormat = *logFormat
	}

	// Shutdown timeout
	var finalShutdownTimeout time.Duration = 30 * time.Second
	if fileCfg != nil && fileCfg.ShutdownTimeout != "" {
		if d, err := time.ParseDuration(fileCfg.ShutdownTimeout); err == nil {
			finalShutdownTimeout = d
		}
	}
	if flagSet["shutdown-timeout"] {
		finalShutdownTimeout = *shutdownTimeout
	}

	// Initialize structured logger
	var slogLevel slog.Level
	switch strings.ToLower(finalLogLevel) {
	case "debug":
		slogLevel = slog.LevelDebug
	case "info":
		slogLevel = slog.LevelInfo
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		log.Fatalf("error: invalid log level %q (use debug, info, warn, or error)", finalLogLevel)
	}

	handlerOpts := &slog.HandlerOptions{Level: slogLevel}
	var logger *slog.Logger
	switch strings.ToLower(finalLogFormat) {
	case "json":
		logger = slog.New(slog.NewJSONHandler(os.Stderr, handlerOpts))
	case "text":
		logger = slog.New(slog.NewTextHandler(os.Stderr, handlerOpts))
	default:
		log.Fatalf("error: invalid log format %q (use text or json)", finalLogFormat)
	}

	// Security warnings
	if finalTLSCert == "" || finalTLSKey == "" {
		logger.Warn("TLS is not configured. All traffic including passwords and screen content will be transmitted in plaintext.")
	}
	if len(finalOrigins) == 0 {
		logger.Warn("No allowed origins configured. WebSocket connections from any origin will be accepted.")
	}

	config := wsproxy.Config{
		ListenAddr:              finalListen,
		AllowedVNCTargets:       finalTargets,
		DefaultVNCPassword:      vncPassword,
		MaxConnections:          finalMaxConns,
		MaxConnectionsPerTarget: finalMaxPerTgt,
		DefaultUploadDir:        finalDefaultDir,
		AllowedUploadDirs:       finalUpDirs,
		MaxUploadSize:           finalMaxUpload,
		TLSCertFile:             finalTLSCert,
		TLSKeyFile:              finalTLSKey,
		AllowedOrigins:          finalOrigins,
		ShutdownTimeout:         finalShutdownTimeout,
		Logger:                  logger,
	}

	server := wsproxy.NewServer(config)
	if err := server.ListenAndServe(); err != nil {
		log.Printf("server error: %v", err)
		os.Exit(1)
	}
}
