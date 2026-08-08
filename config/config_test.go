package config

import (
	"testing"
	"time"
)

// --- Defaults ---

func TestLoad_Defaults(t *testing.T) {
	// No environment variables set; expect defaults.
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Port != defaultPort {
		t.Errorf("Load() Port = %d, want %d", cfg.Port, defaultPort)
	}
	if cfg.ShutdownTimeout != defaultShutdownTimeout {
		t.Errorf("Load() ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, defaultShutdownTimeout)
	}
}

func TestLoad_Addr_Default(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Addr() != ":8080" {
		t.Errorf("Addr() = %q, want %q", cfg.Addr(), ":8080")
	}
}

// --- Valid values ---

func TestLoad_ValidPort(t *testing.T) {
	t.Setenv("APP_PORT", "9090")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Port != 9090 {
		t.Errorf("Load() Port = %d, want 9090", cfg.Port)
	}
	if cfg.Addr() != ":9090" {
		t.Errorf("Addr() = %q, want %q", cfg.Addr(), ":9090")
	}
}

func TestLoad_ValidShutdownTimeout(t *testing.T) {
	t.Setenv("APP_SHUTDOWN_TIMEOUT", "30s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("Load() ShutdownTimeout = %v, want 30s", cfg.ShutdownTimeout)
	}
}

func TestLoad_PortBoundaries(t *testing.T) {
	cases := []struct {
		port string
		want int
	}{
		{"1", 1},
		{"1024", 1024},
		{"65535", 65535},
	}

	for _, tc := range cases {
		t.Run("port="+tc.port, func(t *testing.T) {
			t.Setenv("APP_PORT", tc.port)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if cfg.Port != tc.want {
				t.Errorf("Load() Port = %d, want %d", cfg.Port, tc.want)
			}
		})
	}
}

func TestLoad_ShutdownTimeoutFormats(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"5s", 5 * time.Second},
		{"1m", time.Minute},
		{"500ms", 500 * time.Millisecond},
	}

	for _, tc := range cases {
		t.Run("timeout="+tc.raw, func(t *testing.T) {
			t.Setenv("APP_SHUTDOWN_TIMEOUT", tc.raw)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if cfg.ShutdownTimeout != tc.want {
				t.Errorf("Load() ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, tc.want)
			}
		})
	}
}

// --- Invalid values ---

func TestLoad_InvalidPort_NonInteger(t *testing.T) {
	t.Setenv("APP_PORT", "abc")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for APP_PORT=abc, got nil")
	}
}

func TestLoad_InvalidPort_Zero(t *testing.T) {
	t.Setenv("APP_PORT", "0")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for APP_PORT=0, got nil")
	}
}

func TestLoad_InvalidPort_Negative(t *testing.T) {
	t.Setenv("APP_PORT", "-1")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for APP_PORT=-1, got nil")
	}
}

func TestLoad_InvalidPort_TooLarge(t *testing.T) {
	t.Setenv("APP_PORT", "65536")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for APP_PORT=65536, got nil")
	}
}

func TestLoad_InvalidShutdownTimeout_NotADuration(t *testing.T) {
	t.Setenv("APP_SHUTDOWN_TIMEOUT", "invalid")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for APP_SHUTDOWN_TIMEOUT=invalid, got nil")
	}
}

func TestLoad_InvalidShutdownTimeout_Zero(t *testing.T) {
	t.Setenv("APP_SHUTDOWN_TIMEOUT", "0s")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for APP_SHUTDOWN_TIMEOUT=0s, got nil")
	}
}

func TestLoad_InvalidShutdownTimeout_Negative(t *testing.T) {
	t.Setenv("APP_SHUTDOWN_TIMEOUT", "-5s")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for APP_SHUTDOWN_TIMEOUT=-5s, got nil")
	}
}
