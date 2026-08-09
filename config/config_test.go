package config

import (
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
