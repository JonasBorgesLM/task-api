package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRealIP_StoresResolvedAddressInContext(t *testing.T) {
	var got string
	addressOf := func(*http.Request) (string, error) { return "203.0.113.7", nil }
	handler := RealIP(addressOf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = RealIPFromContext(r.Context())
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if got != "203.0.113.7" {
		t.Errorf("RealIPFromContext() = %q, want %q", got, "203.0.113.7")
	}
}

// TestRealIP_ResolutionErrorStoresEmptyString pins that a failure to
// resolve the address never fails the request itself — this is a value
// for logging, never something anything downstream is gated on.
func TestRealIP_ResolutionErrorStoresEmptyString(t *testing.T) {
	var got string
	// Returns a non-empty value *alongside* the error, on purpose: this
	// is what actually exercises the "ignore the value when err != nil"
	// guard. A fake returning ("", err) would pass even if that guard
	// were deleted, since the zero value already happens to be "".
	addressOf := func(*http.Request) (string, error) { return "some-leftover-value", errors.New("malformed header") }
	handler := RealIP(addressOf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = RealIPFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if got != "" {
		t.Errorf("RealIPFromContext() after a resolution error = %q, want \"\"", got)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d — a resolution error must not fail the request", w.Code, http.StatusOK)
	}
}

func TestRealIPFromContext_EmptyWhenMiddlewareNeverRan(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	if got := RealIPFromContext(req.Context()); got != "" {
		t.Errorf("RealIPFromContext() on a bare context = %q, want \"\"", got)
	}
}
