package config

import (
	"log/slog"
	"slices"
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
	if cfg.CORSAllowedOrigins != nil {
		t.Errorf("Load() CORSAllowedOrigins = %v, want nil (CORS disabled by default)", cfg.CORSAllowedOrigins)
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

// --- CORS_ALLOWED_ORIGINS ---

func TestLoad_CORSAllowedOrigins_Unset(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.CORSAllowedOrigins != nil {
		t.Errorf("Load() CORSAllowedOrigins = %v, want nil", cfg.CORSAllowedOrigins)
	}
}

func TestLoad_CORSAllowedOrigins_Single(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:8082")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	want := []string{"http://localhost:8082"}
	if !slices.Equal(cfg.CORSAllowedOrigins, want) {
		t.Errorf("Load() CORSAllowedOrigins = %v, want %v", cfg.CORSAllowedOrigins, want)
	}
}

func TestLoad_CORSAllowedOrigins_MultipleWithWhitespace(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:8082, https://app.example.com ,,http://localhost:3000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	want := []string{"http://localhost:8082", "https://app.example.com", "http://localhost:3000"}
	if !slices.Equal(cfg.CORSAllowedOrigins, want) {
		t.Errorf("Load() CORSAllowedOrigins = %v, want %v", cfg.CORSAllowedOrigins, want)
	}
}

func TestLoad_CORSAllowedOrigins_EmptyString_IsUnset(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.CORSAllowedOrigins != nil {
		t.Errorf("Load() CORSAllowedOrigins = %v, want nil", cfg.CORSAllowedOrigins)
	}
}

// --- HSTS_MAX_AGE ---

func TestLoad_HSTSMaxAge_Unset_SendsTheHeader(t *testing.T) {
	// Unset means "send it with the default max-age", not "omit it". The
	// header is inert over plaintext, and the alternative — deciding per
	// request from r.TLS — silently disables HSTS behind a
	// TLS-terminating proxy, which is where this binary always runs.
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.HSTSMaxAge != defaultHSTSMaxAge {
		t.Errorf("Load() HSTSMaxAge = %v, want %v", cfg.HSTSMaxAge, defaultHSTSMaxAge)
	}
}

func TestLoad_HSTSMaxAge_Set(t *testing.T) {
	t.Setenv("HSTS_MAX_AGE", "8760h")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if want := 8760 * time.Hour; cfg.HSTSMaxAge != want {
		t.Errorf("Load() HSTSMaxAge = %v, want %v", cfg.HSTSMaxAge, want)
	}
}

func TestLoad_HSTSMaxAge_Invalid(t *testing.T) {
	t.Setenv("HSTS_MAX_AGE", "one-year")

	if _, err := Load(); err == nil {
		t.Error("Load() error = nil, want an error for a non-duration HSTS_MAX_AGE")
	}
}

func TestLoad_HSTSMaxAge_ZeroDisablesTheHeader(t *testing.T) {
	// Zero is the documented opt-out for a service that is deliberately
	// and permanently plaintext. It is the one duration in this config
	// where zero is a setting rather than a mistake, which is why it has
	// its own parser — the header is then omitted, not sent as
	// max-age=0.
	t.Setenv("HSTS_MAX_AGE", "0s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.HSTSMaxAge != 0 {
		t.Errorf("Load() HSTSMaxAge = %v, want 0", cfg.HSTSMaxAge)
	}
}

func TestLoad_HSTSMaxAge_NegativeIsRejected(t *testing.T) {
	t.Setenv("HSTS_MAX_AGE", "-1h")

	if _, err := Load(); err == nil {
		t.Error("Load() error = nil, want an error for a negative HSTS_MAX_AGE")
	}
}

// --- rate limits ---

// TestLoad_RateLimits_Defaults pins that every tier gets a usable value
// when nothing is set. A zero here would not mean "unlimited": ratelimit
// treats a non-positive refill rate as a bucket that never refills, so a
// tier defaulted to zero would serve one request and then reject
// everything.
func TestLoad_RateLimits_Defaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	checks := []struct {
		name  string
		burst int
		rate  float64
	}{
		{"global", cfg.RateLimitBurst, cfg.RateLimitPerSec},
		{"auth", cfg.AuthRateLimitBurst, cfg.AuthRateLimitPerSec},
		{"user", cfg.UserRateLimitBurst, cfg.UserRateLimitPerSec},
	}
	for _, c := range checks {
		if c.burst <= 0 {
			t.Errorf("%s burst = %d, want a positive default", c.name, c.burst)
		}
		if c.rate <= 0 {
			t.Errorf("%s per-second = %v, want a positive default", c.name, c.rate)
		}
	}
}

func TestLoad_RateLimits_RejectNonPositive(t *testing.T) {
	vars := []string{
		"RATE_LIMIT_BURST",
		"AUTH_RATE_LIMIT_BURST",
		"USER_RATE_LIMIT_BURST",
		"RATE_LIMIT_PER_SEC",
		"AUTH_RATE_LIMIT_PER_SEC",
		"USER_RATE_LIMIT_PER_SEC",
	}
	for _, name := range vars {
		for _, value := range []string{"0", "-1"} {
			t.Run(name+"="+value, func(t *testing.T) {
				t.Setenv(name, value)

				if _, err := Load(); err == nil {
					t.Errorf("Load() error = nil, want an error for %s=%s", name, value)
				}
			})
		}
	}
}

func TestLoad_RateLimits_RejectNonNumeric(t *testing.T) {
	t.Setenv("RATE_LIMIT_PER_SEC", "twenty")

	if _, err := Load(); err == nil {
		t.Error("Load() error = nil, want an error for a non-numeric RATE_LIMIT_PER_SEC")
	}
}

// TestLoad_RateLimits_RejectNaN covers the value that would otherwise
// parse cleanly and then poison every comparison it takes part in: NaN is
// neither greater nor less than any limit, so a bucket configured with it
// makes no decision at all.
func TestLoad_RateLimits_RejectNaN(t *testing.T) {
	t.Setenv("RATE_LIMIT_PER_SEC", "NaN")

	if _, err := Load(); err == nil {
		t.Error("Load() error = nil, want an error for RATE_LIMIT_PER_SEC=NaN")
	}
}

func TestLoad_RateLimits_Set(t *testing.T) {
	t.Setenv("RATE_LIMIT_BURST", "5")
	t.Setenv("RATE_LIMIT_PER_SEC", "2.5")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.RateLimitBurst != 5 {
		t.Errorf("RateLimitBurst = %d, want 5", cfg.RateLimitBurst)
	}
	if cfg.RateLimitPerSec != 2.5 {
		t.Errorf("RateLimitPerSec = %v, want 2.5", cfg.RateLimitPerSec)
	}
}

// --- TRUSTED_PROXIES ---

func TestLoad_TrustedProxies_UnsetIsNil(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.TrustedProxies != nil {
		t.Errorf("TrustedProxies = %v, want nil — no peer is trusted unless declared", cfg.TrustedProxies)
	}
}

func TestLoad_TrustedProxies_AcceptsCIDRsAndBareAddresses(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, 2001:db8::/32, 192.168.1.7")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	want := []string{"10.0.0.0/8", "2001:db8::/32", "192.168.1.7"}
	if len(cfg.TrustedProxies) != len(want) {
		t.Fatalf("TrustedProxies = %v, want %v", cfg.TrustedProxies, want)
	}
	for i := range want {
		if cfg.TrustedProxies[i] != want[i] {
			t.Errorf("TrustedProxies[%d] = %q, want %q", i, cfg.TrustedProxies[i], want[i])
		}
	}
}

func TestLoad_TrustedProxies_RejectsGarbage(t *testing.T) {
	for _, value := range []string{"not-an-ip", "10.0.0.0/33", "10.0.0.0/8,nope"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("TRUSTED_PROXIES", value)

			if _, err := Load(); err == nil {
				t.Errorf("Load() error = nil, want an error for TRUSTED_PROXIES=%q", value)
			}
		})
	}
}

// TestLoad_TrustedProxies_RejectsDefaultRoute covers the entry that looks
// like configuration and is actually a bypass: trusting 0.0.0.0/0 makes
// every client a proxy, so every client gets to choose its own
// rate-limit identity.
func TestLoad_TrustedProxies_RejectsDefaultRoute(t *testing.T) {
	for _, value := range []string{"0.0.0.0/0", "::/0", "10.0.0.0/8,0.0.0.0/0"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("TRUSTED_PROXIES", value)

			if _, err := Load(); err == nil {
				t.Errorf("Load() error = nil, want a refusal for TRUSTED_PROXIES=%q", value)
			}
		})
	}
}
