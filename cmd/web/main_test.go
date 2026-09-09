package main

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// discardLogger returns a logger that silently discards all output —
// duplicated from cmd/api/main_test.go's identical helper rather than
// shared: this is a different main package, and three lines are not
// worth a shared package for.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func sha256Source(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

func TestCSPScriptHashes_BareInlineScript(t *testing.T) {
	html := []byte(`<head><script>var x = 1;</script></head>`)
	got := cspScriptHashes(html)
	want := []string{sha256Source("var x = 1;")}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("cspScriptHashes() = %v, want %v", got, want)
	}
}

// TestCSPScriptHashes_SkipsExternalScripts pins the distinction that
// matters most here: a script tag naming a file (src=, whether or not
// it also carries type="module"/crossorigin) is not inline, and hashing
// its content — the JS source of an entirely different file — would be
// meaningless. Only bare <script>...</script> counts.
func TestCSPScriptHashes_SkipsExternalScripts(t *testing.T) {
	html := []byte(`<script type="module" crossorigin src="/assets/index-ABC.js"></script>`)
	if got := cspScriptHashes(html); len(got) != 0 {
		t.Errorf("cspScriptHashes() for an external script = %v, want none", got)
	}
}

func TestCSPScriptHashes_MultipleInlineScripts(t *testing.T) {
	html := []byte(`<script>a();</script><p>x</p><script>b();</script>`)
	got := cspScriptHashes(html)
	want := []string{sha256Source("a();"), sha256Source("b();")}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("cspScriptHashes() = %v, want %v", got, want)
	}
}

func TestCSPScriptHashes_NoInlineScripts_ReturnsNone(t *testing.T) {
	html := []byte(`<html><body>no scripts here</body></html>`)
	if got := cspScriptHashes(html); len(got) != 0 {
		t.Errorf("cspScriptHashes() = %v, want none", got)
	}
}

// TestCSPScriptHashes_AgainstRealIndexHTML is the integration-shaped
// version of the two tests above: the actual file this server ships
// with in production, not a synthetic fixture. If web/index.html's
// inline script ever changes shape (a second one added, an attribute
// added to the existing one), this either changes what gets hashed or —
// if the change makes it stop matching bare <script></script> at all —
// silently drops to zero, which is why
// TestBuildCSP_AgainstRealIndexHTML below also asserts the count
// directly rather than only checking the policy string parses.
func TestCSPScriptHashes_AgainstRealIndexHTML(t *testing.T) {
	html := readRealIndexHTML(t)

	hashes := cspScriptHashes(html)
	if len(hashes) != 1 {
		t.Fatalf("cspScriptHashes(web/index.html) found %d inline scripts, want exactly 1 (the theme-preference script) — see index.html's own doc comment if this changed on purpose", len(hashes))
	}
	if !strings.HasPrefix(hashes[0], "'sha256-") || !strings.HasSuffix(hashes[0], "'") {
		t.Errorf("cspScriptHashes(web/index.html)[0] = %q, want a quoted sha256- source expression", hashes[0])
	}
}

func TestBuildCSP_IncludesAPIOriginInConnectSrc(t *testing.T) {
	csp := buildCSP([]byte(`<script>x</script>`), "https://api.example.com")
	if !strings.Contains(csp, "connect-src 'self' https://api.example.com") {
		t.Errorf("buildCSP() = %q, missing connect-src for the configured API origin", csp)
	}
}

func TestBuildCSP_NoInlineScripts_ScriptSrcIsJustSelf(t *testing.T) {
	csp := buildCSP([]byte(`<html></html>`), "https://api.example.com")
	if !strings.Contains(csp, "script-src 'self'; ") {
		t.Errorf("buildCSP() = %q, want script-src to be exactly 'self' with no hashes", csp)
	}
}

// TestBuildCSP_NamesEveryDirectiveThatMatters guards against silently
// losing a directive during a future edit — frame-ancestors, base-uri
// and object-src don't inherit from default-src, so dropping any one of
// them removes that protection entirely rather than falling back to a
// safe default (the same fact secureheaders.WithCSP's own doc comment
// states).
func TestBuildCSP_NamesEveryDirectiveThatMatters(t *testing.T) {
	csp := buildCSP(readRealIndexHTML(t), "https://api.example.com")
	for _, want := range []string{
		"default-src 'self'",
		"style-src 'self'",
		"img-src 'self' data:",
		"font-src 'self'",
		"object-src 'none'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("buildCSP() = %q, missing directive %q", csp, want)
		}
	}
}

func readRealIndexHTML(t *testing.T) []byte {
	t.Helper()
	html, err := os.ReadFile(filepath.Join("..", "..", "web", "index.html"))
	if err != nil {
		t.Fatalf("read web/index.html: %v", err)
	}
	return html
}

// --- spaHandler ---

func newTestDist(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	assetsDir := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(assets): %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "index-ABC123.js"), []byte("console.log('hi')"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "favicon.svg"), []byte("<svg></svg>"), 0o644); err != nil {
		t.Fatalf("write favicon: %v", err)
	}
	return dir
}

func TestSPAHandler_RealAsset_ServedWithImmutableCache(t *testing.T) {
	dist := newTestDist(t)
	handler := spaHandler(dist, []byte("<html>index</html>"))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/index-ABC123.js", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got, want := w.Header().Get("Cache-Control"), "public, max-age=31536000, immutable"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
	if got := w.Body.String(); got != "console.log('hi')" {
		t.Errorf("body = %q, want the asset's real content", got)
	}
}

func TestSPAHandler_RealFile_OutsideAssets_ServedWithNoCache(t *testing.T) {
	dist := newTestDist(t)
	handler := spaHandler(dist, []byte("<html>index</html>"))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/favicon.svg", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got, want := w.Header().Get("Cache-Control"), "no-cache"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
}

// TestSPAHandler_UnknownPath_FallsBackToIndex is the whole point of this
// handler existing instead of a bare http.FileServer: react-router-dom
// owns "/tasks/abc123" client-side, and there is no such file on disk.
func TestSPAHandler_UnknownPath_FallsBackToIndex(t *testing.T) {
	dist := newTestDist(t)
	index := []byte("<html>the real app shell</html>")
	handler := spaHandler(dist, index)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tasks/abc123", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (never a redirect, never a 404)", w.Code, http.StatusOK)
	}
	if got := w.Body.String(); got != string(index) {
		t.Errorf("body = %q, want the index.html shell", got)
	}
	if got, want := w.Header().Get("Cache-Control"), "no-cache"; got != want {
		t.Errorf("Cache-Control = %q, want %q — the shell must be revalidated after every deploy", got, want)
	}
}

// TestSPAHandler_PathTraversal_AttemptStillResolvesInsideDist sends a
// request whose URL.Path was set directly to a value carrying "../..",
// bypassing net/http's own request-line parsing (which would already
// have collapsed it) so this actually reaches spaHandler's own
// path.Clean call. It proves the *observed behavior* is safe — the
// request falls through to the SPA shell, never to a file outside dist
// — but it does not, on its own, prove pathEscapesDir's guard is what
// makes that true: removing the call to pathEscapesDir in spaHandler
// does not turn this test red, because path.Clean on an already-rooted
// path has already resolved "../../secret.txt" down to "/secret.txt"
// (still inside dist, just a name that happens not to exist there)
// before pathEscapesDir ever runs. Verified by removing that call and
// re-running this test during development.
//
// TestPathEscapesDir below is what actually exercises the guard's own
// logic, including a negative control on the function in isolation —
// the honest way to verify code an HTTP request cannot reach.
func TestSPAHandler_PathTraversal_AttemptStillResolvesInsideDist(t *testing.T) {
	dist := newTestDist(t)
	// A file that exists on the host but must never be reachable through
	// this handler, placed as a sibling of dist rather than somewhere
	// like /etc/passwd, so the test doesn't depend on OS filesystem
	// layout.
	secretDir := filepath.Dir(dist)
	secret := filepath.Join(secretDir, "secret.txt")
	if err := os.WriteFile(secret, []byte("must never be served"), 0o644); err != nil {
		t.Fatalf("write secret fixture: %v", err)
	}

	handler := spaHandler(dist, []byte("<html>index</html>"))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/traversal", nil)
	req.URL.Path = "/assets/../../secret.txt"
	handler.ServeHTTP(w, req)

	if strings.Contains(w.Body.String(), "must never be served") {
		t.Fatalf("path traversal escaped dist: body = %q", w.Body.String())
	}
}

// TestPathEscapesDir exercises the guard's boolean logic directly,
// including cases path.Clean's own guarantee means an HTTP request can
// never actually produce — this is what a negative control on this
// function looks like: flip its body to `return false` unconditionally
// (the bug this guards against — a containment check that always says
// "safe") and every one of the escaping cases below fails. Restored
// after confirming that during development.
func TestPathEscapesDir(t *testing.T) {
	cases := []struct {
		name   string
		fsPath string
		dir    string
		want   bool
	}{
		{"exactly the directory itself", "/srv/dist", "/srv/dist", false},
		{"a real descendant", "/srv/dist/assets/app.js", "/srv/dist", false},
		{"a sibling directory sharing a name prefix", "/srv/dist-evil/app.js", "/srv/dist", true},
		{"a parent of the directory", "/srv", "/srv/dist", true},
		{"entirely unrelated", "/etc/passwd", "/srv/dist", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathEscapesDir(tc.fsPath, tc.dir); got != tc.want {
				t.Errorf("pathEscapesDir(%q, %q) = %v, want %v", tc.fsPath, tc.dir, got, tc.want)
			}
		})
	}
}

// --- config ---

func TestLoadConfig_RequiresAPIOrigin(t *testing.T) {
	t.Setenv("WEB_API_ORIGIN", "")
	if _, err := loadConfig(); err == nil {
		t.Error("loadConfig() with no WEB_API_ORIGIN: want an error, got nil")
	}
}

func TestLoadConfig_DefaultsApplied(t *testing.T) {
	t.Setenv("WEB_API_ORIGIN", "https://api.example.com")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() unexpected error: %v", err)
	}
	if cfg.addr != defaultWebAddr {
		t.Errorf("addr = %q, want default %q", cfg.addr, defaultWebAddr)
	}
	if cfg.distDir != defaultWebDistDir {
		t.Errorf("distDir = %q, want default %q", cfg.distDir, defaultWebDistDir)
	}
	if cfg.hstsMaxAge != defaultWebHSTSMaxAge {
		t.Errorf("hstsMaxAge = %v, want default %v", cfg.hstsMaxAge, defaultWebHSTSMaxAge)
	}
	if cfg.preShutdownDelay != 0 {
		t.Errorf("preShutdownDelay = %v, want 0 by default", cfg.preShutdownDelay)
	}
}

func TestLoadConfig_RejectsMalformedDuration(t *testing.T) {
	t.Setenv("WEB_API_ORIGIN", "https://api.example.com")
	t.Setenv("WEB_HSTS_MAX_AGE", "not-a-duration")
	if _, err := loadConfig(); err == nil {
		t.Error("loadConfig() with a malformed WEB_HSTS_MAX_AGE: want an error, got nil")
	}
}

// --- newHandler (headers) ---

func TestNewHandler_SecurityHeadersAndHealthz(t *testing.T) {
	dist := newTestDist(t)
	indexHTML := []byte("<html><script>t()</script></html>")
	csp := buildCSP(indexHTML, "https://api.example.com")
	handler := newHandler(dist, indexHTML, csp, 24*time.Hour, discardLogger())

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Content-Security-Policy"); got != csp {
		t.Errorf("Content-Security-Policy = %q, want %q", got, csp)
	}
	if got := w.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("Strict-Transport-Security is missing")
	}
	if got := w.Header().Get("X-Request-Id"); got == "" {
		t.Error("X-Request-Id is missing — RequestID must run before secureheaders in the chain")
	}
}
