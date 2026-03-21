package wsproxy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFileConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	content := `{
  "listen": ":9090",
  "tls": {
    "cert": "/etc/cert.pem",
    "key": "/etc/key.pem",
    "require": true
  },
  "security": {
    "allowed_vnc_targets": ["10.0.0.1:5900"],
    "allowed_origins": ["https://example.com"],
    "rate_limit": {
      "enabled": true,
      "max_attempts": 3,
      "window": "10m",
      "lockout": "1m"
    }
  },
  "limits": {
    "max_connections": 50,
    "max_connections_per_target": 5,
    "max_upload_size": "200MB"
  },
  "uploads": {
    "default_dir": "/tmp/uploads",
    "allowed_dirs": ["/tmp/uploads"]
  },
  "logging": {
    "level": "debug",
    "format": "json"
  },
  "shutdown_timeout": "60s"
}`

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFileConfig(path)
	if err != nil {
		t.Fatalf("LoadFileConfig: %v", err)
	}

	if cfg.Listen != ":9090" {
		t.Errorf("Listen = %q, want :9090", cfg.Listen)
	}
	if cfg.TLS == nil || cfg.TLS.Cert != "/etc/cert.pem" {
		t.Errorf("TLS.Cert = %v, want /etc/cert.pem", cfg.TLS)
	}
	if cfg.Security == nil || len(cfg.Security.AllowedVNCTargets) != 1 {
		t.Errorf("Security.AllowedVNCTargets = %v, want 1 target", cfg.Security)
	}
	if cfg.Limits == nil || cfg.Limits.MaxConnections != 50 {
		t.Errorf("Limits.MaxConnections = %v, want 50", cfg.Limits)
	}
	if cfg.Logging == nil || cfg.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %v, want debug", cfg.Logging)
	}
	if cfg.ShutdownTimeout != "60s" {
		t.Errorf("ShutdownTimeout = %q, want 60s", cfg.ShutdownTimeout)
	}
}

func TestLoadFileConfig_Invalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte(`{invalid`), 0644)

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadFileConfig_NotFound(t *testing.T) {
	_, err := LoadFileConfig("/nonexistent/config.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestValidateFileConfig(t *testing.T) {
	tests := []struct {
		name     string
		cfg      FileConfig
		wantErrs int
	}{
		{
			name:     "valid minimal",
			cfg:      FileConfig{Security: &SecurityFileConfig{AllowAnyTarget: true}},
			wantErrs: 0,
		},
		{
			name: "TLS require without certs",
			cfg: FileConfig{
				TLS:      &TLSFileConfig{Require: true},
				Security: &SecurityFileConfig{AllowAnyTarget: true},
			},
			wantErrs: 1,
		},
		{
			name: "TLS cert without key",
			cfg: FileConfig{
				TLS:      &TLSFileConfig{Cert: "cert.pem"},
				Security: &SecurityFileConfig{AllowAnyTarget: true},
			},
			wantErrs: 1,
		},
		{
			name: "no targets and no allow-any",
			cfg: FileConfig{
				Security: &SecurityFileConfig{},
			},
			wantErrs: 1,
		},
		{
			name: "invalid rate limit window",
			cfg: FileConfig{
				Security: &SecurityFileConfig{
					AllowAnyTarget: true,
					RateLimit:      &RateLimitFileConfig{Window: "bad"},
				},
			},
			wantErrs: 1,
		},
		{
			name: "invalid upload size",
			cfg: FileConfig{
				Security: &SecurityFileConfig{AllowAnyTarget: true},
				Limits:   &LimitsFileConfig{MaxUploadSize: "notasize"},
			},
			wantErrs: 1,
		},
		{
			name: "invalid log level",
			cfg: FileConfig{
				Security: &SecurityFileConfig{AllowAnyTarget: true},
				Logging:  &LoggingFileConfig{Level: "verbose"},
			},
			wantErrs: 1,
		},
		{
			name: "invalid shutdown timeout",
			cfg: FileConfig{
				Security:        &SecurityFileConfig{AllowAnyTarget: true},
				ShutdownTimeout: "notaduration",
			},
			wantErrs: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateFileConfig(&tt.cfg)
			if len(errs) != tt.wantErrs {
				t.Errorf("got %d errors, want %d: %v", len(errs), tt.wantErrs, errs)
			}
		})
	}
}

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		input string
		want  int64
		err   bool
	}{
		{"100", 100, false},
		{"100B", 100, false},
		{"1KB", 1024, false},
		{"1K", 1024, false},
		{"100MB", 100 * 1024 * 1024, false},
		{"1GB", 1024 * 1024 * 1024, false},
		{"", 0, true},
		{"notasize", 0, true},
		{"100XB", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseByteSize(tt.input)
			if tt.err && err == nil {
				t.Fatal("expected error")
			}
			if !tt.err && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	cfg := &FileConfig{}

	t.Setenv("REDVNC_LISTEN", ":7070")
	t.Setenv("REDVNC_TLS_CERT", "/env/cert.pem")
	t.Setenv("REDVNC_TLS_KEY", "/env/key.pem")
	t.Setenv("REDVNC_VNC_PASSWORD", "envpass")
	t.Setenv("REDVNC_MAX_CONNECTIONS", "200")
	t.Setenv("REDVNC_LOG_LEVEL", "warn")
	t.Setenv("REDVNC_LOG_FORMAT", "json")
	t.Setenv("REDVNC_SHUTDOWN_TIMEOUT", "45s")

	ApplyEnvOverrides(cfg)

	if cfg.Listen != ":7070" {
		t.Errorf("Listen = %q, want :7070", cfg.Listen)
	}
	if cfg.TLS == nil || cfg.TLS.Cert != "/env/cert.pem" || cfg.TLS.Key != "/env/key.pem" {
		t.Errorf("TLS = %v, want cert and key from env", cfg.TLS)
	}
	if cfg.Security == nil || cfg.Security.VNCPassword != "envpass" {
		t.Errorf("Security.VNCPassword = %v, want envpass", cfg.Security)
	}
	if cfg.Limits == nil || cfg.Limits.MaxConnections != 200 {
		t.Errorf("Limits.MaxConnections = %v, want 200", cfg.Limits)
	}
	if cfg.Logging == nil || cfg.Logging.Level != "warn" || cfg.Logging.Format != "json" {
		t.Errorf("Logging = %v, want warn/json", cfg.Logging)
	}
	if cfg.ShutdownTimeout != "45s" {
		t.Errorf("ShutdownTimeout = %q, want 45s", cfg.ShutdownTimeout)
	}
}
