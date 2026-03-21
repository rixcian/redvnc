package wsproxy

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// FileConfig represents the JSON configuration file structure.
type FileConfig struct {
	Listen string          `json:"listen"`
	TLS    *TLSFileConfig  `json:"tls,omitempty"`
	Security *SecurityFileConfig `json:"security,omitempty"`
	Limits   *LimitsFileConfig   `json:"limits,omitempty"`
	Uploads  *UploadsFileConfig  `json:"uploads,omitempty"`
	Logging  *LoggingFileConfig  `json:"logging,omitempty"`
	ShutdownTimeout string `json:"shutdown_timeout,omitempty"`
}

// TLSFileConfig holds TLS settings from the config file.
type TLSFileConfig struct {
	Cert    string `json:"cert"`
	Key     string `json:"key"`
	Require bool   `json:"require"`
}

// SecurityFileConfig holds security settings from the config file.
type SecurityFileConfig struct {
	AllowedVNCTargets []string       `json:"allowed_vnc_targets"`
	AllowAnyTarget    bool           `json:"allow_any_target"`
	AllowedOrigins    []string       `json:"allowed_origins"`
	VNCPassword       string         `json:"vnc_password"`
	VNCPasswordFile   string         `json:"vnc_password_file"`
	RateLimit         *RateLimitFileConfig `json:"rate_limit,omitempty"`
}

// RateLimitFileConfig holds rate limit settings from the config file.
type RateLimitFileConfig struct {
	Enabled     *bool  `json:"enabled,omitempty"`
	MaxAttempts int    `json:"max_attempts,omitempty"`
	Window      string `json:"window,omitempty"`
	Lockout     string `json:"lockout,omitempty"`
}

// LimitsFileConfig holds connection limit settings from the config file.
type LimitsFileConfig struct {
	MaxConnections          int    `json:"max_connections,omitempty"`
	MaxConnectionsPerTarget int    `json:"max_connections_per_target,omitempty"`
	MaxUploadSize           string `json:"max_upload_size,omitempty"`
}

// UploadsFileConfig holds upload settings from the config file.
type UploadsFileConfig struct {
	DefaultDir  string   `json:"default_dir"`
	AllowedDirs []string `json:"allowed_dirs"`
}

// LoggingFileConfig holds logging settings from the config file.
type LoggingFileConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

// LoadFileConfig reads and parses a JSON configuration file.
func LoadFileConfig(path string) (*FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	return &cfg, nil
}

// ValidateFileConfig checks the config for errors and returns all issues at once.
func ValidateFileConfig(cfg *FileConfig) []string {
	var errs []string

	if cfg.TLS != nil {
		if cfg.TLS.Require && (cfg.TLS.Cert == "" || cfg.TLS.Key == "") {
			errs = append(errs, "tls.require is true but tls.cert and/or tls.key are not set")
		}
		if (cfg.TLS.Cert == "") != (cfg.TLS.Key == "") {
			errs = append(errs, "tls.cert and tls.key must both be set or both be empty")
		}
	}

	if cfg.Security != nil {
		if len(cfg.Security.AllowedVNCTargets) == 0 && !cfg.Security.AllowAnyTarget {
			errs = append(errs, "security.allowed_vnc_targets is empty and security.allow_any_target is false")
		}
		if cfg.Security.RateLimit != nil {
			if cfg.Security.RateLimit.Window != "" {
				if _, err := time.ParseDuration(cfg.Security.RateLimit.Window); err != nil {
					errs = append(errs, fmt.Sprintf("security.rate_limit.window: invalid duration %q", cfg.Security.RateLimit.Window))
				}
			}
			if cfg.Security.RateLimit.Lockout != "" {
				if _, err := time.ParseDuration(cfg.Security.RateLimit.Lockout); err != nil {
					errs = append(errs, fmt.Sprintf("security.rate_limit.lockout: invalid duration %q", cfg.Security.RateLimit.Lockout))
				}
			}
		}
	}

	if cfg.Limits != nil {
		if cfg.Limits.MaxUploadSize != "" {
			if _, err := ParseByteSize(cfg.Limits.MaxUploadSize); err != nil {
				errs = append(errs, fmt.Sprintf("limits.max_upload_size: %v", err))
			}
		}
	}

	if cfg.Logging != nil {
		if cfg.Logging.Level != "" {
			switch strings.ToLower(cfg.Logging.Level) {
			case "debug", "info", "warn", "error":
			default:
				errs = append(errs, fmt.Sprintf("logging.level: invalid value %q (use debug, info, warn, or error)", cfg.Logging.Level))
			}
		}
		if cfg.Logging.Format != "" {
			switch strings.ToLower(cfg.Logging.Format) {
			case "text", "json":
			default:
				errs = append(errs, fmt.Sprintf("logging.format: invalid value %q (use text or json)", cfg.Logging.Format))
			}
		}
	}

	if cfg.ShutdownTimeout != "" {
		if _, err := time.ParseDuration(cfg.ShutdownTimeout); err != nil {
			errs = append(errs, fmt.Sprintf("shutdown_timeout: invalid duration %q", cfg.ShutdownTimeout))
		}
	}

	return errs
}

// ParseByteSize parses a human-readable byte size like "100MB", "1GB", "512KB".
func ParseByteSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}

	// Find where the number ends and the unit begins
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}

	numStr := s[:i]
	unit := strings.ToUpper(strings.TrimSpace(s[i:]))

	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number in size %q", s)
	}

	var multiplier float64
	switch unit {
	case "", "B":
		multiplier = 1
	case "KB", "K":
		multiplier = 1024
	case "MB", "M":
		multiplier = 1024 * 1024
	case "GB", "G":
		multiplier = 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("unknown size unit %q in %q", unit, s)
	}

	return int64(num * multiplier), nil
}

// ApplyEnvOverrides applies REDVNC_ environment variable overrides to the config.
func ApplyEnvOverrides(cfg *FileConfig) {
	if v := os.Getenv("REDVNC_LISTEN"); v != "" {
		cfg.Listen = v
	}

	if v := os.Getenv("REDVNC_TLS_CERT"); v != "" {
		if cfg.TLS == nil {
			cfg.TLS = &TLSFileConfig{}
		}
		cfg.TLS.Cert = v
	}
	if v := os.Getenv("REDVNC_TLS_KEY"); v != "" {
		if cfg.TLS == nil {
			cfg.TLS = &TLSFileConfig{}
		}
		cfg.TLS.Key = v
	}

	if v := os.Getenv("REDVNC_VNC_PASSWORD"); v != "" {
		if cfg.Security == nil {
			cfg.Security = &SecurityFileConfig{}
		}
		cfg.Security.VNCPassword = v
	}

	if v := os.Getenv("REDVNC_MAX_CONNECTIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if cfg.Limits == nil {
				cfg.Limits = &LimitsFileConfig{}
			}
			cfg.Limits.MaxConnections = n
		}
	}

	if v := os.Getenv("REDVNC_MAX_UPLOAD_SIZE"); v != "" {
		if cfg.Limits == nil {
			cfg.Limits = &LimitsFileConfig{}
		}
		cfg.Limits.MaxUploadSize = v
	}

	if v := os.Getenv("REDVNC_LOG_LEVEL"); v != "" {
		if cfg.Logging == nil {
			cfg.Logging = &LoggingFileConfig{}
		}
		cfg.Logging.Level = v
	}

	if v := os.Getenv("REDVNC_LOG_FORMAT"); v != "" {
		if cfg.Logging == nil {
			cfg.Logging = &LoggingFileConfig{}
		}
		cfg.Logging.Format = v
	}

	if v := os.Getenv("REDVNC_SHUTDOWN_TIMEOUT"); v != "" {
		cfg.ShutdownTimeout = v
	}
}
