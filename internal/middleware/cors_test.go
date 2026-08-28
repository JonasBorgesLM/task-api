package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newCORSTestHandler() (http.Handler, *bool) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	return next, &called
}

// --- Disabled (zero value): must be a complete no-op ---

func TestCORS_Disabled_NoHeadersAndRequestPassesThrough(t *testing.T) {
	next, called := newCORSTestHandler()
	handler := CORS(nil)(next)

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Origin", "http://localhost:8082")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !*called {
		t.Error("disabled CORS must let the request reach the wrapped handler")
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("disabled CORS set Access-Control-Allow-Origin = %q, want none", got)
	}
}

func TestCORS_Disabled_OptionsAlsoPassesThrough(t *testing.T) {
	// With CORS disabled, an OPTIONS request is not this middleware's
	// concern at all — it must reach the wrapped handler (mux) exactly as
	// it did before this middleware existed, typically 404ing there since
	// no route registers OPTIONS.
	next, called := newCORSTestHandler()
	handler := CORS(nil)(next)

	req := httptest.NewRequest(http.MethodOptions, "/tasks", nil)
	req.Header.Set("Origin", "http://localhost:8082")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !*called {
		t.Error("disabled CORS must let an OPTIONS request reach the wrapped handler too")
	}
}

// --- Enabled, origin not in the allowlist: behaves exactly like disabled ---

func TestCORS_Enabled_DisallowedOrigin_NoHeaders(t *testing.T) {
	next, called := newCORSTestHandler()
	handler := CORS([]string{"http://localhost:8082"})(next)

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !*called {
		t.Error("a disallowed origin must still reach the wrapped handler")
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("disallowed origin got Access-Control-Allow-Origin = %q, want none", got)
	}
}

func TestCORS_Enabled_NoOriginHeader_NoHeaders(t *testing.T) {
	// A same-origin or server-to-server request never sends Origin.
	next, called := newCORSTestHandler()
	handler := CORS([]string{"http://localhost:8082"})(next)

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !*called {
		t.Error("a request with no Origin header must still reach the wrapped handler")
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("request with no Origin got Access-Control-Allow-Origin = %q, want none", got)
	}
}

// --- Enabled, allowed origin: actual (non-preflight) request ---

func TestCORS_Enabled_AllowedOrigin_SetsHeadersAndCallsNext(t *testing.T) {
	next, called := newCORSTestHandler()
	handler := CORS([]string{"http://localhost:8082"})(next)

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Origin", "http://localhost:8082")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !*called {
		t.Error("an allowed origin's actual request must still reach the wrapped handler")
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:8082" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "http://localhost:8082")
	}
	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q", got, "Origin")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (from the wrapped handler)", w.Code, http.StatusOK)
	}
}

func TestCORS_Enabled_MultipleAllowedOrigins_EachEchoedBack(t *testing.T) {
	handler := CORS([]string{"http://localhost:8082", "https://app.example.com"})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
	)

	for _, origin := range []string{"http://localhost:8082", "https://app.example.com"} {
		req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("origin %q: Access-Control-Allow-Origin = %q, want %q", origin, got, origin)
		}
	}
}

// --- Enabled, allowed origin: preflight (OPTIONS) request ---

func TestCORS_Enabled_AllowedOriginPreflight_ShortCircuitsWithNoContent(t *testing.T) {
	next, called := newCORSTestHandler()
	handler := CORS([]string{"http://localhost:8082"})(next)

	req := httptest.NewRequest(http.MethodOptions, "/tasks", nil)
	req.Header.Set("Origin", "http://localhost:8082")
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if *called {
		t.Error("a preflight request must be answered by CORS itself, not reach the wrapped handler")
	}
	if w.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:8082" {
		t.Errorf("preflight Access-Control-Allow-Origin = %q, want %q", got, "http://localhost:8082")
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("preflight response is missing Access-Control-Allow-Methods")
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("preflight response is missing Access-Control-Allow-Headers")
	}
	if got := w.Header().Get("Access-Control-Max-Age"); got == "" {
		t.Error("preflight response is missing Access-Control-Max-Age")
	}
}

func TestCORS_Enabled_AllowedOriginPreflight_UnroutedPathStillAnswered(t *testing.T) {
	// The whole point of intercepting OPTIONS ahead of mux dispatch: a
	// preflight for a path/method that IS otherwise registered (e.g.
	// POST /tasks, which has no OPTIONS handler of its own) must still
	// succeed, not fall through to a 404 from the router.
	handler := CORS([]string{"http://localhost:8082"})(
		http.NewServeMux(), // deliberately empty — nothing is routed
	)

	req := httptest.NewRequest(http.MethodOptions, "/tasks", nil)
	req.Header.Set("Origin", "http://localhost:8082")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d — CORS must answer preflight before routing", w.Code, http.StatusNoContent)
	}
}

// --- Vary: Origin ---
//
// These pin a real bug: Vary: Origin used to be set only inside the
// allowed-origin branch, leaving a disallowed-origin or no-Origin
// response cacheable without it. A shared cache could then store one of
// those and later serve it to a browser whose Origin *is* allowed —
// silently missing Access-Control-Allow-Origin, read by that browser as
// a cross-origin failure with nothing wrong on this server's side.

func TestCORS_Enabled_AllowedOrigin_SetsVary(t *testing.T) {
	next, _ := newCORSTestHandler()
	handler := CORS([]string{"http://localhost:8082"})(next)

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Origin", "http://localhost:8082")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q", got, "Origin")
	}
}

func TestCORS_Enabled_DisallowedOrigin_StillSetsVary(t *testing.T) {
	next, _ := newCORSTestHandler()
	handler := CORS([]string{"http://localhost:8082"})(next)

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q — a disallowed origin's response still depends on Origin", got, "Origin")
	}
}

func TestCORS_Enabled_NoOriginHeader_StillSetsVary(t *testing.T) {
	next, _ := newCORSTestHandler()
	handler := CORS([]string{"http://localhost:8082"})(next)

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q — a request with no Origin still depends on whether one is present", got, "Origin")
	}
}

func TestCORS_Disabled_NeverSetsVary(t *testing.T) {
	// CORS is a complete no-op when disabled (see CORS's doc comment) —
	// Vary: Origin would be actively wrong here, since the response
	// never depends on Origin when there's no allow-list to check it
	// against.
	next, _ := newCORSTestHandler()
	handler := CORS(nil)(next)

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Origin", "http://localhost:8082")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("Vary"); got != "" {
		t.Errorf("Vary = %q, want none — CORS is disabled", got)
	}
}
