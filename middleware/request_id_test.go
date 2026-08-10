package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestID_GeneratesIDWhenHeaderAbsent(t *testing.T) {
	var gotFromContext string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFromContext, _ = RequestIDFromContext(r.Context())
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	got := w.Header().Get(HeaderRequestID)
	if got == "" {
		t.Fatal("RequestID() did not set the response header")
	}
	if gotFromContext != got {
		t.Errorf("RequestIDFromContext() = %q, want %q (must match response header)", gotFromContext, got)
	}
}

func TestRequestID_GeneratesDifferentIDsPerRequest(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/", nil))

	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/", nil))

	id1 := w1.Header().Get(HeaderRequestID)
	id2 := w2.Header().Get(HeaderRequestID)
	if id1 == id2 {
		t.Errorf("RequestID() generated the same ID twice: %q", id1)
	}
}

func TestRequestID_ReusesValidClientSuppliedID(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(HeaderRequestID, "client-supplied-id-123")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get(HeaderRequestID); got != "client-supplied-id-123" {
		t.Errorf("RequestID() = %q, want the reused client-supplied ID %q", got, "client-supplied-id-123")
	}
}

func TestRequestID_ContextMatchesClientSuppliedID(t *testing.T) {
	var gotFromContext string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFromContext, _ = RequestIDFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(HeaderRequestID, "client-supplied-id-123")

	handler.ServeHTTP(httptest.NewRecorder(), req)

	if gotFromContext != "client-supplied-id-123" {
		t.Errorf("RequestIDFromContext() = %q, want %q", gotFromContext, "client-supplied-id-123")
	}
}

func TestRequestID_RejectsInvalidClientSuppliedID(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"too long", strings.Repeat("a", maxRequestIDLen+1)},
		{"contains space", "has space"},
		{"contains CRLF", "id\r\nX-Injected: evil"},
		{"contains unicode", "id-☃"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(HeaderRequestID, tc.raw)

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			got := w.Header().Get(HeaderRequestID)
			if got == tc.raw {
				t.Errorf("RequestID() reused invalid client ID %q instead of generating a new one", tc.raw)
			}
			if got == "" {
				t.Error("RequestID() must still generate a fresh ID when the client-supplied one is invalid")
			}
		})
	}
}

func TestIsValidRequestID(t *testing.T) {
	cases := []struct {
		name string
		id   string
		want bool
	}{
		{"empty", "", false},
		{"simple alnum", "abc123", true},
		{"with hyphen and underscore", "abc-123_def", true},
		{"too long", strings.Repeat("a", maxRequestIDLen+1), false},
		{"exactly max length", strings.Repeat("a", maxRequestIDLen), true},
		{"contains space", "abc 123", false},
		{"contains newline", "abc\n123", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isValidRequestID(tc.id); got != tc.want {
				t.Errorf("isValidRequestID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}
