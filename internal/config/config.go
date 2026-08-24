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
//	DATABASE_URL            PostgreSQL connection string (e.g.
//	                        postgres://user:pass@host:5432/dbname?sslmode=disable).
//	                        When unset, the application falls back to an
//	                        in-memory task store (default: unset)
//	DB_MAX_OPEN_CONNS       Maximum open connections in the database pool (default: 25)
//	DB_MAX_IDLE_CONNS       Maximum idle connections kept in the database pool (default: 25)
//	DB_CONN_MAX_LIFETIME    Maximum lifetime of a pooled database connection,
//	                        as a Go duration string (default: 5m)
//	DB_AUTO_MIGRATE         Whether to apply pending PostgreSQL migrations on
//	                        startup: true or false (default: true)
//	AUTH_SESSION_TTL        How long a session token issued by POST /auth/login
//	                        remains valid, as a Go duration string (default: 24h)
//	CORS_ALLOWED_ORIGINS    Comma-separated list of origins (e.g.
//	                        http://localhost:8082) a browser-based client is
//	                        allowed to call this API from. Unset (the
//	                        default) disables CORS entirely: no
//	                        Access-Control-* headers are added and behavior
//	                        is unchanged from before this setting existed —
//	                        the right default for a server-to-server or
//	                        same-origin client, which is every client this
//	                        API served until CORS support was added.
//	HSTS_MAX_AGE            How long browsers should refuse to reach this
//	                        API over plaintext, as a Go duration string
//	                        (e.g. 8760h for a year). Unset (the default)
//	                        omits Strict-Transport-Security entirely; set
//	                        it only when HTTPS actually terminates in front
//	                        of this process. See middleware.SecurityHeaders.
package config

import (
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/netip"
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
	defaultDBMaxOpenConns  = 25
	defaultDBMaxIdleConns  = 25
	defaultDBConnMaxLife   = 5 * time.Minute
	defaultDBAutoMigrate   = true
	defaultAuthSessionTTL  = 24 * time.Hour

	// defaultHSTSMaxAge mirrors secureheaders.DefaultHSTSMaxAge — one
	// year, the shortest span browsers and the preload list treat as a
	// serious commitment. It is duplicated here rather than imported so
	// that config keeps depending on nothing but the standard library;
	// the value is asserted against the library's own constant in
	// cmd/api's tests.
	defaultHSTSMaxAge = 365 * 24 * time.Hour

	// Rate-limit defaults, in token-bucket terms: burst is the depth of
	// the bucket (how many requests may arrive at once) and perSec the
	// rate it refills at (the sustained throughput allowed afterwards).
	//
	// Three tiers, from the broadest to the narrowest, because they
	// answer different threats — see newServer in cmd/api/main.go for how
	// they compose:
	//
	//   - Global, keyed by client IP: a coarse ceiling on any single
	//     source, applied before authentication so it also bounds the
	//     session lookup RequireAuth performs.
	//   - Auth, keyed by client IP, on /auth/register and /auth/login
	//     only: tight enough to blunt credential stuffing, loose enough
	//     that a user retyping a password never notices.
	//   - User, keyed by authenticated user ID: bounds what one account
	//     can do regardless of how many addresses it connects from.
	defaultRateLimitBurst     = 60
	defaultRateLimitPerSec    = 20.0
	defaultAuthRateLimitBurst = 10
	defaultAuthRateLimitPer   = 0.05
	defaultUserRateLimitBurst = 120
	defaultUserRateLimitPer   = 40.0
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

	// DatabaseURL is a PostgreSQL connection string. When empty (the
	// zero value), the application uses an in-memory task store instead
	// of PostgreSQL — this is what every test that builds a
	// config.Config{} without setting it relies on.
	DatabaseURL string

	// DBMaxOpenConns is the maximum number of open connections in the
	// PostgreSQL connection pool (sql.DB.SetMaxOpenConns). Unused when
	// DatabaseURL is empty.
	DBMaxOpenConns int

	// DBMaxIdleConns is the maximum number of idle connections kept in
	// the PostgreSQL connection pool (sql.DB.SetMaxIdleConns). Unused
	// when DatabaseURL is empty.
	DBMaxIdleConns int

	// DBConnMaxLifetime is the maximum lifetime of a pooled PostgreSQL
	// connection (sql.DB.SetConnMaxLifetime) before it is closed and
	// replaced, bounding how long a connection can survive a database
	// failover or load balancer change. Unused when DatabaseURL is empty.
	DBConnMaxLifetime time.Duration

	// DBAutoMigrate controls whether the application applies pending
	// PostgreSQL migrations (see migrate.RunMigrations) on startup. Unused
	// when DatabaseURL is empty.
	DBAutoMigrate bool

	// AuthSessionTTL is how long a session token issued by
	// user.Service.CreateSession (POST /auth/login) remains valid before
	// user.Service.ValidateToken rejects it.
	AuthSessionTTL time.Duration

	// CORSAllowedOrigins is the set of origins a browser-based client may
	// call this API from (see middleware.CORS). A nil/empty slice — the
	// zero value, and what every test building a bare config.Config{}
	// gets — disables CORS entirely.
	CORSAllowedOrigins []string

	// HSTSMaxAge is the max-age carried by the Strict-Transport-Security
	// response header. Load defaults it to defaultHSTSMaxAge, so the
	// header is sent unless an operator explicitly sets HSTS_MAX_AGE=0.
	//
	// Sending it unconditionally is safe even though this binary always
	// serves plaintext: RFC 6797 §7.2 requires a user agent to ignore the
	// header when it arrives over a non-secure transport, so a plaintext
	// response carrying it changes nothing. The alternative — suppressing
	// it unless r.TLS is set — looks more careful but is actively wrong
	// here, because TLS terminates at a proxy and r.TLS is nil for
	// requests that genuinely reached the client over HTTPS. That variant
	// disables HSTS exactly where it was meant to apply.
	//
	// Zero is the documented opt-out for a service that is deliberately
	// and permanently plaintext; it omits the header rather than sending
	// max-age=0, which would instruct browsers to forget an existing
	// policy.
	HSTSMaxAge time.Duration

	// TrustedProxies lists the peers that are reverse proxies this
	// deployment operates, as CIDRs ("10.0.0.0/8", "2001:db8::/32") or
	// bare addresses. Empty — the zero value — means no peer is trusted
	// and the rate limiters key on the peer address alone.
	//
	// This exists because the address-keyed tiers are only as good as
	// their key. Behind a proxy the peer address is the *proxy's*, so
	// every client collapses into one bucket; reading X-Forwarded-For
	// unconditionally instead is worse, because the client writes that
	// header and could then mint a fresh identity per request. A
	// forwarding header becomes usable exactly when the peer is known to
	// be your own infrastructure, which is what this list declares.
	//
	// Getting it wrong in the other direction is the dangerous mistake:
	// listing a *client* range here hands those clients the ability to
	// choose their own rate-limit identity. The default route is
	// rejected outright for that reason.
	TrustedProxies []string

	// Rate-limit tiers. Burst is the token bucket's depth, PerSec the
	// rate it refills at; see the defaults above for what each tier is
	// for and cmd/api/main.go's newServer for how they are composed.
	// Every one of these must be positive — Load rejects zero and
	// negative values rather than reading them as "unlimited", so a
	// typo can only ever over-restrict.
	RateLimitBurst      int
	RateLimitPerSec     float64
	AuthRateLimitBurst  int
	AuthRateLimitPerSec float64
	UserRateLimitBurst  int
	UserRateLimitPerSec float64
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
		Addr:              defaultAddr,
		ReadTimeout:       defaultReadTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
		ShutdownTimeout:   defaultShutdownTimeout,
		LogLevel:          defaultLogLevel,
		DBMaxOpenConns:    defaultDBMaxOpenConns,
		DBMaxIdleConns:    defaultDBMaxIdleConns,
		DBConnMaxLifetime: defaultDBConnMaxLife,
		DBAutoMigrate:     defaultDBAutoMigrate,
		AuthSessionTTL:    defaultAuthSessionTTL,
		HSTSMaxAge:        defaultHSTSMaxAge,
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

	// DATABASE_URL is intentionally not format-validated here: connection
	// strings vary (postgres://..., key=value form, ...) and the
	// PostgreSQL driver is the authority on what it accepts. An invalid
	// value surfaces when the application tries to open/ping the
	// connection at startup, with the driver's own error message.
	cfg.DatabaseURL = os.Getenv("DATABASE_URL")

	if cfg.DBMaxOpenConns, err = parsePositiveInt("DB_MAX_OPEN_CONNS", defaultDBMaxOpenConns); err != nil {
		return Config{}, err
	}
	if cfg.DBMaxIdleConns, err = parsePositiveInt("DB_MAX_IDLE_CONNS", defaultDBMaxIdleConns); err != nil {
		return Config{}, err
	}
	if cfg.DBConnMaxLifetime, err = parseDuration("DB_CONN_MAX_LIFETIME", defaultDBConnMaxLife); err != nil {
		return Config{}, err
	}
	if cfg.DBAutoMigrate, err = parseBool("DB_AUTO_MIGRATE", defaultDBAutoMigrate); err != nil {
		return Config{}, err
	}
	if cfg.AuthSessionTTL, err = parseDuration("AUTH_SESSION_TTL", defaultAuthSessionTTL); err != nil {
		return Config{}, err
	}

	cfg.CORSAllowedOrigins = parseCommaSeparated("CORS_ALLOWED_ORIGINS")

	// Unlike every other duration here, zero is meaningful rather than
	// invalid: it is the explicit opt-out for a permanently-plaintext
	// deployment (see Config.HSTSMaxAge). parseDuration rejects zero, so
	// this one variable gets its own parser.
	if cfg.HSTSMaxAge, err = parseNonNegativeDuration("HSTS_MAX_AGE", defaultHSTSMaxAge); err != nil {
		return Config{}, err
	}

	if cfg.TrustedProxies, err = parseCIDRList("TRUSTED_PROXIES"); err != nil {
		return Config{}, err
	}

	if cfg.RateLimitBurst, err = parsePositiveInt("RATE_LIMIT_BURST", defaultRateLimitBurst); err != nil {
		return Config{}, err
	}
	if cfg.RateLimitPerSec, err = parsePositiveFloat("RATE_LIMIT_PER_SEC", defaultRateLimitPerSec); err != nil {
		return Config{}, err
	}
	if cfg.AuthRateLimitBurst, err = parsePositiveInt("AUTH_RATE_LIMIT_BURST", defaultAuthRateLimitBurst); err != nil {
		return Config{}, err
	}
	if cfg.AuthRateLimitPerSec, err = parsePositiveFloat("AUTH_RATE_LIMIT_PER_SEC", defaultAuthRateLimitPer); err != nil {
		return Config{}, err
	}
	if cfg.UserRateLimitBurst, err = parsePositiveInt("USER_RATE_LIMIT_BURST", defaultUserRateLimitBurst); err != nil {
		return Config{}, err
	}
	if cfg.UserRateLimitPerSec, err = parsePositiveFloat("USER_RATE_LIMIT_PER_SEC", defaultUserRateLimitPer); err != nil {
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

// parsePositiveInt reads the environment variable name and parses it as a
// base-10 integer. If the variable is unset, def is returned unchanged.
// The parsed value must be strictly positive.
func parsePositiveInt(name string, def int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return def, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s %q is not a valid integer: %w", name, raw, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("config: %s must be positive, got %q", name, raw)
	}

	return n, nil
}

// parseCIDRList reads a comma-separated list of CIDRs or bare addresses
// and returns them unchanged, having verified that each one parses. An
// unset variable yields nil, which disables trusted-proxy handling
// entirely.
//
// The entries are returned as strings rather than as netip.Prefix values
// because the consumer (moat's realip.New) takes strings and applies its
// own, authoritative checks. Parsing here is not a substitute for that —
// it is what turns a typo into a startup failure naming the offending
// entry, instead of a limiter that silently keys on the wrong thing.
//
// The default route is rejected: 0.0.0.0/0 or ::/0 makes every client a
// trusted proxy, which does not harden the limiter, it removes it.
func parseCIDRList(name string) ([]string, error) {
	raw := parseCommaSeparated(name)
	if len(raw) == 0 {
		return nil, nil
	}

	for _, entry := range raw {
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			if prefix.Bits() == 0 {
				return nil, fmt.Errorf("config: %s entry %q is the default route, which would trust every client as a proxy", name, entry)
			}
			continue
		}
		// A bare address is accepted too: "trust this one load balancer"
		// is common and awkward to spell as a /32 or /128.
		if _, err := netip.ParseAddr(entry); err != nil {
			return nil, fmt.Errorf("config: %s entry %q is not a valid CIDR or IP address", name, entry)
		}
	}

	return raw, nil
}

// parsePositiveFloat reads the environment variable name and parses it as
// a floating-point number, returning def when the variable is unset. A
// value that is not a number, or that is zero or negative, is an error:
// a rate of zero would mean a bucket that never refills, which reads as
// "misconfigured" far more often than it reads as "deliberately frozen".
// NaN is rejected for the same reason — it silently poisons every
// comparison downstream.
func parsePositiveFloat(name string, def float64) (float64, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return def, nil
	}

	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("config: %s %q is not a valid number: %w", name, raw, err)
	}
	if math.IsNaN(f) || f <= 0 {
		return 0, fmt.Errorf("config: %s must be positive, got %q", name, raw)
	}

	return f, nil
}

// parseNonNegativeDuration is parseDuration for the one variable where
// zero is a meaningful setting rather than a mistake: HSTS_MAX_AGE, where
// it means "omit Strict-Transport-Security entirely". Negative values are
// still rejected.
func parseNonNegativeDuration(name string, def time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return def, nil
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s %q is not a valid duration: %w", name, raw, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("config: %s must not be negative, got %q", name, raw)
	}

	return d, nil
}

// parseBool reads the environment variable name and parses it with
// strconv.ParseBool (accepts 1/t/T/TRUE/true/True and 0/f/F/FALSE/false/False).
// If the variable is unset, def is returned unchanged.
func parseBool(name string, def bool) (bool, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return def, nil
	}

	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("config: %s %q is not a valid boolean: %w", name, raw, err)
	}

	return b, nil
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

// parseCommaSeparated reads the environment variable name and splits it on
// commas, trimming whitespace from each entry and dropping empty ones —
// so "a, b,,c" and "a,b,c" parse identically, and "" or " " parse to nil.
// Unlike this package's other parse* helpers, there is no invalid input to
// reject: any string is a valid (if possibly empty) list, so this never
// returns an error.
func parseCommaSeparated(name string) []string {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}

	var out []string
	for _, entry := range strings.Split(raw, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			out = append(out, entry)
		}
	}
	return out
}
