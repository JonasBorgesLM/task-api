// Command web serves the built SPA (web/dist) as its own process,
// separate from cmd/api. See docs/DECISIONS.md § "SPA deployment: um
// servidor Go próprio, não nginx/Caddy" for why this exists instead of
// an off-the-shelf static-file image, and issue #229 for the gap it
// closes: before this, the frontend only ever ran via `vite dev`, and
// nothing served its production build with any security headers at
// all — the API's own strict CSP protects the API's origin, which
// serves no HTML and could never be the one an XSS runs from.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/JonasBorgesLM/moat/secureheaders"

	"github.com/JonasBorgesLM/task-api/internal/middleware"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(ctx, logger); err != nil {
		logger.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	indexPath := filepath.Join(cfg.distDir, "index.html")
	// #nosec G304 -- indexPath is built from WEB_DIST_DIR, operator-supplied
	// startup configuration (see loadConfig), never attacker input; same
	// reasoning as internal/config/dotenv.go's identical annotation.
	indexHTML, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", indexPath, err)
	}

	csp := buildCSP(indexHTML, cfg.apiOrigin)
	logger.Info("computed content security policy", "policy", csp)

	srv := &http.Server{
		Addr:    cfg.addr,
		Handler: newHandler(cfg.distDir, indexHTML, csp, cfg.hstsMaxAge, logger),

		// Fixed rather than configurable, unlike cmd/api's HTTP_*_TIMEOUT
		// settings: this process only ever serves small, fixed static
		// files, so there is no per-deployment request-timing variance
		// for these to tune. ReadHeaderTimeout in particular is what
		// closes the Slowloris shape — a client that opens a connection
		// and trickles header bytes in to hold it open indefinitely.
		ReadHeaderTimeout: defaultWebReadHeaderTimeout,
		ReadTimeout:       defaultWebReadTimeout,
		WriteTimeout:      defaultWebWriteTimeout,
		IdleTimeout:       defaultWebIdleTimeout,
	}

	// Mirrors cmd/api's own run(): a goroutine drives ListenAndServe, the
	// main path blocks on either that failing or ctx being canceled by a
	// signal, and shutdown drains through the same pre-shutdown-delay +
	// timeout shape — the reasoning is identical (a rolling update's
	// endpoint-removal race), not duplicated by copy-paste ignorance.
	// See cmd/api/main.go's run for the fuller comment on why the delay
	// exists at all.
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("server started", "addr", cfg.addr, "dist_dir", cfg.distDir)
		serverErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		logger.Info("signal received")
	}

	if cfg.preShutdownDelay > 0 {
		logger.Info("draining before shutdown", "delay", cfg.preShutdownDelay.String())
		select {
		case <-time.After(cfg.preShutdownDelay):
		case err := <-serverErr:
			return fmt.Errorf("server error: %w", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-serverErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server error: %w", err)
	}

	logger.Info("shutdown completed")
	return nil
}

// webConfig is this binary's entire configuration surface — small enough
// on purpose that it does not need internal/config's structure (named
// default constants, a shared parser table, four-places-in-sync
// discipline): five settings, three of them optional, read directly in
// loadConfig below.
type webConfig struct {
	addr             string
	distDir          string
	apiOrigin        string
	hstsMaxAge       time.Duration
	shutdownTimeout  time.Duration
	preShutdownDelay time.Duration
}

const (
	defaultWebAddr            = ":8080"
	defaultWebDistDir         = "/dist" // the path web/Dockerfile copies the Vite build to
	defaultWebHSTSMaxAge      = 365 * 24 * time.Hour
	defaultWebShutdownTimeout = 10 * time.Second

	// Not exposed as environment settings — see their use on the
	// http.Server in run() for why fixed values are the right call here.
	defaultWebReadHeaderTimeout = 5 * time.Second
	defaultWebReadTimeout       = 10 * time.Second
	defaultWebWriteTimeout      = 10 * time.Second
	defaultWebIdleTimeout       = 60 * time.Second
)

// loadConfig reads this process's configuration from the environment.
// WEB_API_ORIGIN has no default and its absence is a startup error — see
// its own comment below for why a wrong-but-present value can't be
// caught the same way.
func loadConfig() (webConfig, error) {
	cfg := webConfig{
		addr:            getenvDefault("WEB_ADDR", defaultWebAddr),
		distDir:         getenvDefault("WEB_DIST_DIR", defaultWebDistDir),
		hstsMaxAge:      defaultWebHSTSMaxAge,
		shutdownTimeout: defaultWebShutdownTimeout,
	}

	// The origin the *already-built* SPA calls (baked in at `vite build`
	// time via VITE_API_BASE_URL — see web/src/api/client.ts) has to
	// match this process's connect-src exactly, or the browser blocks
	// every request the app makes to its own API. There is no default
	// that could be correct across environments, and — unlike a
	// malformed DATABASE_URL, which fails a real connection attempt at
	// startup — a wrong-but-non-empty value here fails silently until a
	// browser's console shows a CSP violation. Requiring it non-empty at
	// least catches "forgot it entirely" the same way everything else in
	// this codebase fails at startup rather than at request time.
	cfg.apiOrigin = strings.TrimSpace(os.Getenv("WEB_API_ORIGIN"))
	if cfg.apiOrigin == "" {
		return webConfig{}, errors.New(
			"WEB_API_ORIGIN is required — the origin this server's connect-src " +
				"allows, which must match VITE_API_BASE_URL baked into the build")
	}

	var err error
	if cfg.hstsMaxAge, err = parseDurationDefault("WEB_HSTS_MAX_AGE", defaultWebHSTSMaxAge); err != nil {
		return webConfig{}, err
	}
	if cfg.shutdownTimeout, err = parseDurationDefault("WEB_SHUTDOWN_TIMEOUT", defaultWebShutdownTimeout); err != nil {
		return webConfig{}, err
	}
	if cfg.preShutdownDelay, err = parseDurationDefault("WEB_PRE_SHUTDOWN_DELAY", 0); err != nil {
		return webConfig{}, err
	}

	return cfg, nil
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseDurationDefault(key string, def time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

// inlineScriptPattern matches a bare <script>...</script> tag — no
// attributes at all, the exact shape of index.html's own inline
// theme-preference script (see web/index.html's own doc comment on it).
// A tag carrying any attribute (type="module", src=, crossorigin) names
// an external file already covered by script-src 'self' and is not
// matched here — it needs no hash.
var inlineScriptPattern = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

// cspScriptHashes returns a CSP 'sha256-...' source expression for every
// bare inline <script> in html, in the order they appear.
//
// Computed from the file this process is actually serving, at startup,
// rather than a hash hardcoded as a constant: index.html is a Vite build
// artifact, and a hardcoded value would go stale the moment the inline
// script's content changed — silently blocking dark/light theme
// detection on first paint in production, with nothing at build or
// deploy time positioned to catch it. This way the computed policy is
// always correct for whatever bytes this process actually has open.
func cspScriptHashes(html []byte) []string {
	matches := inlineScriptPattern.FindAllSubmatch(html, -1)
	hashes := make([]string, 0, len(matches))
	for _, m := range matches {
		sum := sha256.Sum256(m[1])
		hashes = append(hashes, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return hashes
}

// buildCSP assembles the Content-Security-Policy for every response this
// server writes. Unlike cmd/api's default-src 'none' (a JSON-only API
// that serves no document and loads nothing), this one has to admit its
// own same-origin scripts, styles and the one data: image
// (web/src/index.css's noise texture) — it is protecting an actual HTML
// document, not refusing to be one.
//
// apiOrigin becomes connect-src's second source: this frontend and its
// API are different origins by design (Fase 12/13's dual-auth-mode), so
// 'self' alone would block every fetch the app makes.
func buildCSP(indexHTML []byte, apiOrigin string) string {
	scriptSrc := "'self'"
	if hashes := cspScriptHashes(indexHTML); len(hashes) > 0 {
		scriptSrc += " " + strings.Join(hashes, " ")
	}

	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + scriptSrc,
		"style-src 'self'",
		"img-src 'self' data:",
		"font-src 'self'",
		"connect-src 'self' " + apiOrigin,
		"object-src 'none'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
	}, "; ")
}

// newHandler builds the complete request chain: RequestID and Logging
// from internal/middleware (generic, no domain knowledge — the same
// reason cmd/api can use them and this genuinely separate binary can
// too, without either importing the other), then secureheaders for CSP/
// HSTS/the rest of the fixed set, then Recovery closest to mux — the
// same order cmd/api's own chain uses, and for the same reason: Logging
// has to sit *outside* Recovery, not the other way around, so a panic
// while serving a file is caught and answered 500 before Logging writes
// its access log line, which is what lets that line report the real,
// recovered status instead of never being written at all.
func newHandler(distDir string, indexHTML []byte, csp string, hstsMaxAge time.Duration, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("/", spaHandler(distDir, indexHTML))

	return middleware.Chain(
		middleware.RequestID,
		middleware.Logging(logger),
		secureheaders.Middleware(
			secureheaders.WithCSP(csp),
			// includeSubdomains/preload stay off for the same reason
			// cmd/api's do (see its own HSTSMaxAge doc comment): this
			// process knows its own scheme, not what else the parent
			// domain serves, and preload is close to irreversible.
			secureheaders.WithHSTS(hstsMaxAge, false, false),
		),
		middleware.Recovery(logger),
	)(mux)
}

// spaHandler serves a real file under distDir when the request path
// names one, and index.html — 200, not a redirect — for every other
// path: react-router-dom's client-side routing means a direct load of
// (say) "/tasks/abc123" has no corresponding file on disk and must fall
// back to the one document every route mounts under, exactly like every
// other SPA server does this.
//
// os.Stat decides which branch, rather than trying http.FileServer
// first and inspecting its response: FileServer's 404 body is written
// for a browser, not for this process's own routing decision, and
// re-parsing it would be a fragile way to ask a question the
// filesystem can already answer directly.
func spaHandler(distDir string, indexHTML []byte) http.Handler {
	fileServer := http.FileServer(http.Dir(distDir))
	cleanDistDir := filepath.Clean(distDir)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// path.Clean on an already-rooted path (r.URL.Path always starts
		// with "/" — Go's ServeMux guarantees it) can never leave a
		// leading ".." in the result, so no request actually reaching
		// this handler can escape distDir this way — verified directly:
		// TestSPAHandler_PathTraversal_AttemptStillResolvesInsideDist
		// sends a raw ".." path and confirms it lands safely inside
		// distDir, and removing pathEscapesDir's *call* below did not
		// turn that test red, because path.Clean had already closed the
		// gap before this line runs.
		//
		// pathEscapesDir stays anyway, as defense in depth against a
		// mistake in *this* function, not in path.Clean — e.g. a future
		// edit that starts building fsPath from a value that skipped
		// path.Clean. TestPathEscapesDir exercises its boolean logic
		// directly, including a negative control on the function itself
		// (see that test's own comment), which is the honest way to
		// prove code that the HTTP-level path cannot actually reach.
		fsPath := filepath.Join(cleanDistDir, filepath.FromSlash(path.Clean(r.URL.Path)))
		if pathEscapesDir(fsPath, cleanDistDir) {
			http.NotFound(w, r)
			return
		}

		if info, err := os.Stat(fsPath); err == nil && !info.IsDir() {
			// Vite hashes every filename under assets/ (index-BusBMSGH.js),
			// so a given URL's content never changes — safe to cache
			// forever. Nothing else here gets that guarantee.
			if strings.HasPrefix(path.Clean(r.URL.Path), "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(indexHTML)
	})
}

// pathEscapesDir reports whether fsPath (already joined with dir) is
// dir itself or a genuine descendant of it. Both dir and fsPath are
// expected already filepath.Clean-ed by the caller; this does not clean
// them itself; that decision is spaHandler's, not this function's.
func pathEscapesDir(fsPath, dir string) bool {
	if fsPath == dir {
		return false
	}
	return !strings.HasPrefix(fsPath, dir+string(filepath.Separator))
}
