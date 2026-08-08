// Package config loads and validates application configuration from environment variables.
//
// Available environment variables:
//
//	APP_PORT               HTTP port the server listens on (default: 8080, range: 1–65535)
//	APP_SHUTDOWN_TIMEOUT   Graceful shutdown timeout as a Go duration string (default: 10s)
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultPort            = 8080
	defaultShutdownTimeout = 10 * time.Second
)

// Config holds all application configuration values.
type Config struct {
	// Port is the TCP port the HTTP server listens on.
	Port int

	// ShutdownTimeout is the maximum duration to wait for in-flight requests
	// to complete when the server receives a shutdown signal.
	ShutdownTimeout time.Duration
}

// Addr returns the server address in the format ":port".
func (c Config) Addr() string {
	return fmt.Sprintf(":%d", c.Port)
}

// Load reads configuration from environment variables and applies defaults
// for any variable that is not set. Returns an error if any value is invalid.
//
// Before reading environment variables, Load calls loadDotEnv(".env") so that
// a local .env file can supply values. Variables already present in the OS
// environment take precedence over the .env file.
func Load() (Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return Config{}, fmt.Errorf("config: failed to load .env: %w", err)
	}

	cfg := Config{
		Port:            defaultPort,
		ShutdownTimeout: defaultShutdownTimeout,
	}

	if raw := os.Getenv("APP_PORT"); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("config: APP_PORT %q is not a valid integer: %w", raw, err)
		}
		if port < 1 || port > 65535 {
			return Config{}, fmt.Errorf("config: APP_PORT %d is out of range (1–65535)", port)
		}
		cfg.Port = port
	}

	if raw := os.Getenv("APP_SHUTDOWN_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("config: APP_SHUTDOWN_TIMEOUT %q is not a valid duration: %w", raw, err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("config: APP_SHUTDOWN_TIMEOUT must be positive, got %q", raw)
		}
		cfg.ShutdownTimeout = d
	}

	return cfg, nil
}
