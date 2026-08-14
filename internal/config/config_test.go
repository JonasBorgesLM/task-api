package config

import (
	"log/slog"
	"testing"
	"time"
)

// --- Defaults ---

func TestLoad_Defaults(t *testing.T) {
	// No environment variables set; expect defaults for every field.
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Addr != defaultAddr {
		t.Errorf("Load() Addr = %q, want %q", cfg.Addr, defaultAddr)
	}
	if cfg.ReadTimeout != defaultReadTimeout {
		t.Errorf("Load() ReadTimeout = %v, want %v", cfg.ReadTimeout, defaultReadTimeout)
	}
	if cfg.WriteTimeout != defaultWriteTimeout {
		t.Errorf("Load() WriteTimeout = %v, want %v", cfg.WriteTimeout, defaultWriteTimeout)
	}
	if cfg.IdleTimeout != defaultIdleTimeout {
		t.Errorf("Load() IdleTimeout = %v, want %v", cfg.IdleTimeout, defaultIdleTimeout)
	}
	if cfg.ShutdownTimeout != defaultShutdownTimeout {
		t.Errorf("Load() ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, defaultShutdownTimeout)
	}
	if cfg.LogLevel != defaultLogLevel {
		t.Errorf("Load() LogLevel = %v, want %v", cfg.LogLevel, defaultLogLevel)
	}
}

// --- Custom values ---

func TestLoad_CustomAddr(t *testing.T) {
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9090" {
		t.Errorf("Load() Addr = %q, want %q", cfg.Addr, "127.0.0.1:9090")
	}
}

func TestLoad_CustomAddr_HostOnlyOmitted(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":9091")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Addr != ":9091" {
		t.Errorf("Load() Addr = %q, want %q", cfg.Addr, ":9091")
	}
}

func TestLoad_CustomTimeouts(t *testing.T) {
	t.Setenv("HTTP_READ_TIMEOUT", "1s")
	t.Setenv("HTTP_WRITE_TIMEOUT", "2s")
	t.Setenv("HTTP_IDLE_TIMEOUT", "3m")
	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "30s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.ReadTimeout != time.Second {
		t.Errorf("Load() ReadTimeout = %v, want %v", cfg.ReadTimeout, time.Second)
	}
	if cfg.WriteTimeout != 2*time.Second {
		t.Errorf("Load() WriteTimeout = %v, want %v", cfg.WriteTimeout, 2*time.Second)
	}
	if cfg.IdleTimeout != 3*time.Minute {
		t.Errorf("Load() IdleTimeout = %v, want %v", cfg.IdleTimeout, 3*time.Minute)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("Load() ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 30*time.Second)
	}
}

func TestLoad_DurationFormats(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"5s", 5 * time.Second},
		{"1m", time.Minute},
		{"500ms", 500 * time.Millisecond},
		{"1h", time.Hour},
	}

	for _, tc := range cases {
		t.Run("duration="+tc.raw, func(t *testing.T) {
			t.Setenv("HTTP_READ_TIMEOUT", tc.raw)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if cfg.ReadTimeout != tc.want {
				t.Errorf("Load() ReadTimeout = %v, want %v", cfg.ReadTimeout, tc.want)
			}
		})
	}
}

func TestLoad_AddrPortBoundaries(t *testing.T) {
	cases := []string{":1", ":1024", ":65535"}

	for _, addr := range cases {
		t.Run("addr="+addr, func(t *testing.T) {
			t.Setenv("HTTP_ADDR", addr)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if cfg.Addr != addr {
				t.Errorf("Load() Addr = %q, want %q", cfg.Addr, addr)
			}
		})
	}
}

// --- Invalid values ---

func TestLoad_InvalidAddr_NoColon(t *testing.T) {
	t.Setenv("HTTP_ADDR", "8080")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for HTTP_ADDR=8080 (missing colon), got nil")
	}
}

func TestLoad_InvalidAddr_NonNumericPort(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":http")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for HTTP_ADDR=:http (non-numeric port), got nil")
	}
}

func TestLoad_InvalidAddr_PortZero(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":0")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for HTTP_ADDR=:0, got nil")
	}
}

func TestLoad_InvalidAddr_PortTooLarge(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":65536")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for HTTP_ADDR=:65536, got nil")
	}
}

func TestLoad_InvalidReadTimeout_NotADuration(t *testing.T) {
	t.Setenv("HTTP_READ_TIMEOUT", "invalid")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for HTTP_READ_TIMEOUT=invalid, got nil")
	}
}

func TestLoad_InvalidWriteTimeout_Zero(t *testing.T) {
	t.Setenv("HTTP_WRITE_TIMEOUT", "0s")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for HTTP_WRITE_TIMEOUT=0s, got nil")
	}
}

func TestLoad_InvalidIdleTimeout_Negative(t *testing.T) {
	t.Setenv("HTTP_IDLE_TIMEOUT", "-1s")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for HTTP_IDLE_TIMEOUT=-1s, got nil")
	}
}

func TestLoad_InvalidShutdownTimeout_NotADuration(t *testing.T) {
	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "not-a-duration")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for HTTP_SHUTDOWN_TIMEOUT=not-a-duration, got nil")
	}
}

func TestLoad_InvalidShutdownTimeout_Zero(t *testing.T) {
	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "0s")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for HTTP_SHUTDOWN_TIMEOUT=0s, got nil")
	}
}

// --- PostgreSQL ---

func TestLoad_DatabaseURL_DefaultsToEmpty(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.DatabaseURL != "" {
		t.Errorf("Load() DatabaseURL = %q, want empty (falls back to in-memory store)", cfg.DatabaseURL)
	}
}

func TestLoad_DatabaseURL_Custom(t *testing.T) {
	const url = "postgres://user:pass@localhost:5432/task_api?sslmode=disable"
	t.Setenv("DATABASE_URL", url)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.DatabaseURL != url {
		t.Errorf("Load() DatabaseURL = %q, want %q", cfg.DatabaseURL, url)
	}
}

func TestLoad_DBPoolDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.DBMaxOpenConns != defaultDBMaxOpenConns {
		t.Errorf("Load() DBMaxOpenConns = %d, want %d", cfg.DBMaxOpenConns, defaultDBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns != defaultDBMaxIdleConns {
		t.Errorf("Load() DBMaxIdleConns = %d, want %d", cfg.DBMaxIdleConns, defaultDBMaxIdleConns)
	}
	if cfg.DBConnMaxLifetime != defaultDBConnMaxLife {
		t.Errorf("Load() DBConnMaxLifetime = %v, want %v", cfg.DBConnMaxLifetime, defaultDBConnMaxLife)
	}
	if cfg.DBAutoMigrate != defaultDBAutoMigrate {
		t.Errorf("Load() DBAutoMigrate = %v, want %v", cfg.DBAutoMigrate, defaultDBAutoMigrate)
	}
}

func TestLoad_DBPoolCustom(t *testing.T) {
	t.Setenv("DB_MAX_OPEN_CONNS", "50")
	t.Setenv("DB_MAX_IDLE_CONNS", "5")
	t.Setenv("DB_CONN_MAX_LIFETIME", "15m")
	t.Setenv("DB_AUTO_MIGRATE", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.DBMaxOpenConns != 50 {
		t.Errorf("Load() DBMaxOpenConns = %d, want 50", cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns != 5 {
		t.Errorf("Load() DBMaxIdleConns = %d, want 5", cfg.DBMaxIdleConns)
	}
	if cfg.DBConnMaxLifetime != 15*time.Minute {
		t.Errorf("Load() DBConnMaxLifetime = %v, want %v", cfg.DBConnMaxLifetime, 15*time.Minute)
	}
	if cfg.DBAutoMigrate != false {
		t.Errorf("Load() DBAutoMigrate = %v, want false", cfg.DBAutoMigrate)
	}
}

func TestLoad_InvalidDBMaxOpenConns_NotAnInteger(t *testing.T) {
	t.Setenv("DB_MAX_OPEN_CONNS", "not-a-number")

	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for DB_MAX_OPEN_CONNS=not-a-number, got nil")
	}
}

func TestLoad_InvalidDBMaxIdleConns_Zero(t *testing.T) {
	t.Setenv("DB_MAX_IDLE_CONNS", "0")

	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for DB_MAX_IDLE_CONNS=0, got nil")
	}
}

func TestLoad_InvalidDBConnMaxLifetime_NotADuration(t *testing.T) {
	t.Setenv("DB_CONN_MAX_LIFETIME", "not-a-duration")

	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for DB_CONN_MAX_LIFETIME=not-a-duration, got nil")
	}
}

func TestLoad_InvalidDBAutoMigrate_NotABool(t *testing.T) {
	t.Setenv("DB_AUTO_MIGRATE", "not-a-bool")

	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for DB_AUTO_MIGRATE=not-a-bool, got nil")
	}
}

// --- LOG_LEVEL ---

func TestLoad_LogLevel(t *testing.T) {
	cases := []struct {
		raw  string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"Error", slog.LevelError},
	}

	for _, tc := range cases {
		t.Run("level="+tc.raw, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tc.raw)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if cfg.LogLevel != tc.want {
				t.Errorf("Load() LogLevel = %v, want %v", cfg.LogLevel, tc.want)
			}
		})
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "verbose")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for LOG_LEVEL=verbose, got nil")
	}
}

// --- DOTENV_PATH ---

func TestLoad_DotenvPathOverride(t *testing.T) {
	// HTTP_ADDR is set indirectly by loadDotEnv (via os.Setenv), not by
	// t.Setenv, so it must be unset manually or it would leak into later
	// tests in this package.
	unsetAfterTest(t, "HTTP_ADDR")

	path := writeDotEnv(t, "HTTP_ADDR=:9099\n")
	t.Setenv("DOTENV_PATH", path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Addr != ":9099" {
		t.Errorf("Load() Addr = %q, want %q (from DOTENV_PATH file)", cfg.Addr, ":9099")
	}
}

func TestLoad_DotenvPath_MissingFileIsNotAnError(t *testing.T) {
	t.Setenv("DOTENV_PATH", t.TempDir()+"/does-not-exist.env")

	if _, err := Load(); err != nil {
		t.Fatalf("Load() with a missing DOTENV_PATH file: unexpected error: %v", err)
	}
}
