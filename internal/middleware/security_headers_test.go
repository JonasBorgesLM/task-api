package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// okHandler is the wrapped handler under test: it records that it ran and
// writes a plain 200, so a test can tell "the middleware set the header"
// from "the middleware swallowed the request".
func okHandler(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

// --- The three unconditional headers ---

func TestSecurityHeaders_SetsUnconditionalHeaders(t *testing.T) {
	var called bool
	handler := SecurityHeaders(0)(okHandler(&called))

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("SecurityHeaders must let the request reach the wrapped handler")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (from the wrapped handler)", w.Code, http.StatusOK)
	}

	want := map[string]string{
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
		"X-Frame-Options":         "DENY",
		"X-Content-Type-Options":  "nosniff",
	}
	for name, wantValue := range want {
		if got := w.Header().Get(name); got != wantValue {
			t.Errorf("%s = %q, want %q", name, got, wantValue)
		}
	}
}

// --- Strict-Transport-Security: opt-in, and off by default ---

func TestSecurityHeaders_OmitsHSTSWhenMaxAgeIsZero(t *testing.T) {
	// The zero value is what a bare config.Config{} yields, and what an
	// unset HSTS_MAX_AGE loads as. A process serving plain HTTP must not
	// claim a TLS guarantee it cannot make — see SecurityHeaders' doc
	// comment on RFC 6797 §7.2.
	var called bool
	handler := SecurityHeaders(0)(okHandler(&called))

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("Strict-Transport-Security = %q, want it absent when max-age is zero", got)
	}
}

func TestSecurityHeaders_SetsHSTSInSecondsWhenConfigured(t *testing.T) {
	// The config value is a Go duration, but the header is defined in
	// seconds — a duration emitted verbatim ("8760h0m0s") would be a
	// malformed header value that browsers discard silently.
	var called bool
	handler := SecurityHeaders(365 * 24 * time.Hour)(okHandler(&called))

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got, want := w.Header().Get("Strict-Transport-Security"), "max-age=31536000"; got != want {
		t.Errorf("Strict-Transport-Security = %q, want %q", got, want)
	}
}

func TestSecurityHeaders_HSTSCarriesNoSubdomainOrPreloadDirective(t *testing.T) {
	// Both reach past this service to the whole domain, and preload is
	// effectively irreversible — they belong to whoever terminates TLS,
	// not to this process. See SecurityHeaders' doc comment.
	var called bool
	handler := SecurityHeaders(time.Hour)(okHandler(&called))

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got, want := w.Header().Get("Strict-Transport-Security"), "max-age=3600"; got != want {
		t.Errorf("Strict-Transport-Security = %q, want exactly %q", got, want)
	}
}

// --- Responses that never reach a handler ---

func TestSecurityHeaders_SetOnResponseThatSkipsTheHandler(t *testing.T) {
	// The headers are set on the way in precisely so a response written
	// by something other than the wrapped handler still carries them: a
	// CORS preflight answered by CORS itself, Recovery's 500 after a
	// panic, or — as here — a 404 from a router with nothing registered.
	handler := SecurityHeaders(0)(http.NewServeMux())

	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
	if got := w.Header().Get("Content-Security-Policy"); got == "" {
		t.Error("a response that never reached a handler went out with no Content-Security-Policy")
	}
	if got := w.Header().Get("X-Frame-Options"); got == "" {
		t.Error("a response that never reached a handler went out with no X-Frame-Options")
	}
}

func TestSecurityHeaders_SurviveAPanicRecoveredDownstream(t *testing.T) {
	// Mirrors the real chain's ordering (SecurityHeaders outside
	// Recovery): headers set before the panic must still be on the 500
	// Recovery writes, since recovering does not clear the ResponseWriter.
	logger, _ := newTestLogger()
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	handler := SecurityHeaders(0)(Recovery(logger)(panicking))

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if got := w.Header().Get("Content-Security-Policy"); got == "" {
		t.Error("the 500 written after a recovered panic went out with no Content-Security-Policy")
	}
	if got := w.Header().Get("X-Content-Type-Options"); got == "" {
		t.Error("the 500 written after a recovered panic went out with no X-Content-Type-Options")
	}
}

// --- A handler may still override ---

func TestSecurityHeaders_HandlerCanOverride(t *testing.T) {
	// Set on the way in, so a handler with a genuine reason to differ
	// still has the last word. Nothing in this API does today; the test
	// pins the ordering, not a use case.
	handler := SecurityHeaders(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got, want := w.Header().Get("X-Frame-Options"), "SAMEORIGIN"; got != want {
		t.Errorf("X-Frame-Options = %q, want %q — the handler must be able to override", got, want)
	}
}
