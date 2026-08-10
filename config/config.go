// Package config loads and validates application configuration from
// environment variables. It is the single place in the codebase that reads
// process environment variables directly — every other package receives
// configuration values as plain Go types (time.Duration, string) through
// the Config struct, and never touches os.Getenv itself.
//
// Available environment variables:
//
//	HTTP_ADDR               TCP address the HTTP server listens on (default: ":8080")
//	HTTP_READ_TIMEOUT       http.Server.ReadTimeout as a Go duration string (default: 5s)
//	HTTP_WRITE_TIMEOUT      http.Server.WriteTimeout as a Go duration string (default: 10s)
//	HTTP_IDLE_TIMEOUT       http.Server.IdleTimeout as a Go duration string (default: 60s)
//	HTTP_SHUTDOWN_TIMEOUT   Graceful shutdown timeout as a Go duration string (default: 10s)
//	LOG_LEVEL               Minimum log level: debug, info, warn, or error (default: info)
//	DOTENV_PATH             Path to the .env file Load reads before the OS
//	                        environment (default: ".env", relative to the
//	                        process's current working directory)
package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAddr            = ":8080"
	defaultReadTimeout     = 5 * time.Second
	defaultWriteTimeout    = 10 * time.Second
	defaultIdleTimeout     = 60 * time.Second
	defaultShutdownTimeout = 10 * time.Second
	defaultLogLevel        = slog.LevelInfo
	defaultDotenvPath      = ".env"
)

// Config holds all application configuration values. It is independent of
// the Service, Repository and Handler types: only cmd/api/main.go reads it,
// to build the http.Server.
type Config struct {
	// Addr is the TCP address the HTTP server listens on, in "host:port"
	// form (e.g. ":8080", "0.0.0.0:8080", "localhost:8080").
	Addr string

	// ReadTimeout is http.Server.ReadTimeout.
	ReadTimeout time.Duration

	// WriteTimeout is http.Server.WriteTimeout.
	WriteTimeout time.Duration

	// IdleTimeout is http.Server.IdleTimeout.
	IdleTimeout time.Duration

	// ShutdownTimeout is the maximum duration to wait for in-flight requests
	// to complete when the server receives a shutdown signal.
	ShutdownTimeout time.Duration

	// LogLevel is the minimum slog.Level the application logs at.
	LogLevel slog.Level
}

// Load reads configuration from environment variables and applies defaults
// for any variable that is not set. Returns an error if any value has an
// invalid format.
//
// Before reading environment variables, Load calls loadDotEnv on the path
// named by DOTENV_PATH (default ".env", resolved relative to the process's
// current working directory) so that a local .env file can supply values.
// Variables already present in the OS environment take precedence over the
// .env file.
func Load() (Config, error) {
	dotenvPath := defaultDotenvPath
	if raw := os.Getenv("DOTENV_PATH"); raw != "" {
		dotenvPath = raw
	}
	if err := loadDotEnv(dotenvPath); err != nil {
		return Config{}, fmt.Errorf("config: failed to load %s: %w", dotenvPath, err)
	}

	cfg := Config{
		Addr:            defaultAddr,
		ReadTimeout:     defaultReadTimeout,
		WriteTimeout:    defaultWriteTimeout,
		IdleTimeout:     defaultIdleTimeout,
		ShutdownTimeout: defaultShutdownTimeout,
		LogLevel:        defaultLogLevel,
	}

	if raw := os.Getenv("HTTP_ADDR"); raw != "" {
		if err := validateAddr(raw); err != nil {
			return Config{}, fmt.Errorf("config: HTTP_ADDR %q is invalid: %w", raw, err)
		}
		cfg.Addr = raw
	}

	var err error
	if cfg.ReadTimeout, err = parseDuration("HTTP_READ_TIMEOUT", defaultReadTimeout); err != nil {
		return Config{}, err
	}
	if cfg.WriteTimeout, err = parseDuration("HTTP_WRITE_TIMEOUT", defaultWriteTimeout); err != nil {
		return Config{}, err
	}
	if cfg.IdleTimeout, err = parseDuration("HTTP_IDLE_TIMEOUT", defaultIdleTimeout); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = parseDuration("HTTP_SHUTDOWN_TIMEOUT", defaultShutdownTimeout); err != nil {
		return Config{}, err
	}
	if cfg.LogLevel, err = parseLogLevel("LOG_LEVEL", defaultLogLevel); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// validateAddr reports whether raw is a syntactically valid "host:port"
// address with a numeric port in the 1–65535 range. The host part may be
// empty (e.g. ":8080" binds to all interfaces), matching net.Listen's rules.
func validateAddr(raw string) error {
	_, port, err := net.SplitHostPort(raw)
	if err != nil {
		return fmt.Errorf("must be a host:port address: %w", err)
	}

	p, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("port %q is not a valid integer", port)
	}
	if p < 1 || p > 65535 {
		return fmt.Errorf("port %d is out of range (1–65535)", p)
	}

	return nil
}

// parseDuration reads the environment variable name and parses it as a Go
// duration string. If the variable is unset, def is returned unchanged.
// The parsed duration must be strictly positive.
func parseDuration(name string, def time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return def, nil
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s %q is not a valid duration: %w", name, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("config: %s must be positive, got %q", name, raw)
	}

	return d, nil
}

// parseLogLevel reads the environment variable name and parses it as a
// case-insensitive log level: "debug", "info", "warn" (or "warning"), or
// "error". If the variable is unset, def is returned unchanged.
func parseLogLevel(name string, def slog.Level) (slog.Level, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return def, nil
	}

	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("config: %s %q is not a valid log level (want debug, info, warn, or error)", name, raw)
	}
}
