package middleware

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestLogger returns a *slog.Logger whose JSON output is captured in the
// returned buffer, one line per log call. Shared by logging_test.go and
// recovery_test.go.
func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	return logger, &buf
}

// decodeLogLine decodes a single JSON log line into a generic map for
// field-by-field assertions.
func decodeLogLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to decode log line %q: %v", buf.String(), err)
	}
	return entry
}

func TestLogging_RecordsMethodPathAndStatus(t *testing.T) {
	logger, buf := newTestLogger()

	handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/tasks", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	entry := decodeLogLine(t, buf)
	if entry["method"] != http.MethodPost {
		t.Errorf("logged method = %v, want %v", entry["method"], http.MethodPost)
	}
	if entry["path"] != "/tasks" {
		t.Errorf("logged path = %v, want %v", entry["path"], "/tasks")
	}
	if entry["status"] != float64(http.StatusCreated) {
		t.Errorf("logged status = %v, want %v", entry["status"], http.StatusCreated)
	}
}

func TestLogging_DefaultsStatusTo200WhenWriteHeaderNotCalled(t *testing.T) {
	logger, buf := newTestLogger()

	handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok")) // no explicit WriteHeader
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	entry := decodeLogLine(t, buf)
	if entry["status"] != float64(http.StatusOK) {
		t.Errorf("logged status = %v, want %v", entry["status"], http.StatusOK)
	}
}

func TestLogging_RecordsDuration(t *testing.T) {
	logger, buf := newTestLogger()

	const sleep = 20 * time.Millisecond
	handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(sleep)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	entry := decodeLogLine(t, buf)
	rawDuration, ok := entry["duration"].(string)
	if !ok {
		t.Fatalf("logged duration = %v, want a string", entry["duration"])
	}
	got, err := time.ParseDuration(rawDuration)
	if err != nil {
		t.Fatalf("logged duration %q is not a valid duration: %v", rawDuration, err)
	}
	if got < sleep {
		t.Errorf("logged duration = %v, want >= %v", got, sleep)
	}
}

func TestLogging_RecordsRequestIDWhenPresent(t *testing.T) {
	logger, buf := newTestLogger()

	handler := RequestID(Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	wantID := w.Header().Get(HeaderRequestID)

	entry := decodeLogLine(t, buf)
	if entry["request_id"] != wantID {
		t.Errorf("logged request_id = %v, want %v", entry["request_id"], wantID)
	}
}

func TestLogging_RequestIDEmptyWhenNotSet(t *testing.T) {
	logger, buf := newTestLogger()

	handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	entry := decodeLogLine(t, buf)
	if entry["request_id"] != "" {
		t.Errorf("logged request_id = %v, want empty string when RequestID isn't earlier in the chain", entry["request_id"])
	}
}

// --- Level and "error" field, by status class ---

func TestLogging_LevelAndErrorField_ByStatusClass(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		wantLevel string
		wantError bool
	}{
		{"200 OK", http.StatusOK, "INFO", false},
		{"201 Created", http.StatusCreated, "INFO", false},
		{"204 No Content", http.StatusNoContent, "INFO", false},
		{"400 Bad Request", http.StatusBadRequest, "WARN", true},
		{"404 Not Found", http.StatusNotFound, "WARN", true},
		{"409 Conflict", http.StatusConflict, "WARN", true},
		{"500 Internal Server Error", http.StatusInternalServerError, "ERROR", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logger, buf := newTestLogger()

			handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

			entry := decodeLogLine(t, buf)
			if entry["level"] != tc.wantLevel {
				t.Errorf("logged level = %v, want %v", entry["level"], tc.wantLevel)
			}

			_, hasError := entry["error"]
			if hasError != tc.wantError {
				t.Errorf("logged error field present = %v, want %v (entry: %v)", hasError, tc.wantError, entry)
			}
			if tc.wantError && entry["error"] != http.StatusText(tc.status) {
				t.Errorf("logged error = %v, want %v", entry["error"], http.StatusText(tc.status))
			}
		})
	}
}

// --- Optional interface passthrough (Flusher, Hijacker, Pusher) ---
//
// statusRecorder embeds http.ResponseWriter as an interface field, which
// only promotes the methods declared on http.ResponseWriter itself
// (Header, Write, WriteHeader) — NOT optional interfaces like http.Flusher
// or http.Hijacker that the concrete writer might also implement. Without
// explicit passthrough methods, a handler behind Logging that needs to
// flush a streaming response or hijack the connection would see those
// type assertions fail even though the real underlying ResponseWriter
// supports them.

// hijackableRecorder is a minimal http.ResponseWriter that also implements
// http.Hijacker and http.Pusher, so tests can verify statusRecorder
// delegates to it correctly.
type hijackableRecorder struct {
	http.ResponseWriter
	hijackCalled bool
	pushCalled   bool
	pushTarget   string
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijackCalled = true
	clientConn, serverConn := net.Pipe()
	serverConn.Close() //nolint:errcheck // only the client side is inspected below
	return clientConn, nil, nil
}

func (h *hijackableRecorder) Push(target string, _ *http.PushOptions) error {
	h.pushCalled = true
	h.pushTarget = target
	return nil
}

func TestLogging_Flush_DelegatesToUnderlyingFlusher(t *testing.T) {
	logger, _ := newTestLogger()

	underlying := httptest.NewRecorder() // implements http.Flusher
	handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter behind Logging does not implement http.Flusher")
		}
		f.Flush()
	}))

	handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))

	if !underlying.Flushed {
		t.Error("Flush() on the wrapped ResponseWriter did not reach the underlying httptest.ResponseRecorder")
	}
}

func TestLogging_Flush_NoopWhenUnderlyingDoesNotSupportIt(t *testing.T) {
	logger, _ := newTestLogger()

	// plainRecorder deliberately does NOT implement http.Flusher.
	underlying := &plainRecorder{header: make(http.Header)}
	handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("statusRecorder must always implement http.Flusher itself, even as a no-op")
		}
		f.Flush() // must not panic
	}))

	handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestLogging_Hijack_DelegatesToUnderlyingHijacker(t *testing.T) {
	logger, _ := newTestLogger()

	underlying := &hijackableRecorder{ResponseWriter: httptest.NewRecorder()}
	handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("ResponseWriter behind Logging does not implement http.Hijacker")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("Hijack() unexpected error: %v", err)
		}
		conn.Close()
	}))

	handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))

	if !underlying.hijackCalled {
		t.Error("Hijack() did not reach the underlying ResponseWriter")
	}
}

func TestLogging_Hijack_ErrorsWhenUnderlyingDoesNotSupportIt(t *testing.T) {
	logger, _ := newTestLogger()

	underlying := httptest.NewRecorder() // does not implement http.Hijacker
	handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("statusRecorder must always implement http.Hijacker itself, even to report unsupported")
		}
		if _, _, err := hj.Hijack(); err == nil {
			t.Error("Hijack() on an unsupported underlying writer: want an error, got nil")
		}
	}))

	handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestLogging_Push_DelegatesToUnderlyingPusher(t *testing.T) {
	logger, _ := newTestLogger()

	underlying := &hijackableRecorder{ResponseWriter: httptest.NewRecorder()}
	handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := w.(http.Pusher)
		if !ok {
			t.Fatal("ResponseWriter behind Logging does not implement http.Pusher")
		}
		if err := p.Push("/style.css", nil); err != nil {
			t.Errorf("Push() unexpected error: %v", err)
		}
	}))

	handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))

	if !underlying.pushCalled || underlying.pushTarget != "/style.css" {
		t.Errorf("Push() did not reach the underlying ResponseWriter as expected: called=%v target=%q",
			underlying.pushCalled, underlying.pushTarget)
	}
}

func TestLogging_Push_NotSupportedWhenUnderlyingDoesNotSupportIt(t *testing.T) {
	logger, _ := newTestLogger()

	underlying := httptest.NewRecorder() // does not implement http.Pusher
	handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := w.(http.Pusher)
		if !ok {
			t.Fatal("statusRecorder must always implement http.Pusher itself, even to report unsupported")
		}
		if err := p.Push("/style.css", nil); !errors.Is(err, http.ErrNotSupported) {
			t.Errorf("Push() error = %v, want http.ErrNotSupported", err)
		}
	}))

	handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))
}

// plainRecorder is a minimal http.ResponseWriter with none of the optional
// interfaces (Flusher, Hijacker, Pusher) — used to verify statusRecorder
// degrades gracefully instead of panicking or forwarding to a method that
// doesn't exist.
type plainRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (p *plainRecorder) Header() http.Header         { return p.header }
func (p *plainRecorder) WriteHeader(status int)      { p.status = status }
func (p *plainRecorder) Write(b []byte) (int, error) { return p.body.Write(b) }

// TestLogging_UserIDForLog_RecordedByAHandlerRunningLaterInTheChain pins
// the mechanism Logging.doc-comment describes: a handler running further
// in — standing in for user.RequireAuth, which is wired per-route rather
// than into this global chain (see cmd/api/main.go's newServer) — calls
// RecordUserIDForLog after Logging's own next.ServeHTTP is already in
// flight, and the resulting access-log line still carries user_id.
//
// This is the case a plain context.WithValue cannot handle: a value set
// by an inner middleware is invisible to an outer middleware's own
// reference to the request. If this test used ContextWithUserID instead
// of RecordUserIDForLog to simulate the inner middleware, it would fail
// — that failure is what proves the pointer indirection is doing real
// work, not just adding indirection for its own sake.
func TestLogging_UserIDForLog_RecordedByAHandlerRunningLaterInTheChain(t *testing.T) {
	logger, buf := newTestLogger()

	handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		RecordUserIDForLog(r.Context(), "user-42")
		w.WriteHeader(http.StatusOK)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	entry := decodeLogLine(t, buf)
	if entry["user_id"] != "user-42" {
		t.Errorf("logged user_id = %v, want %q", entry["user_id"], "user-42")
	}
}

// TestLogging_NoUserID_OmitsTheField covers the public/unauthenticated
// path: nothing ever calls RecordUserIDForLog, and the log line must not
// carry an empty user_id — its absence is what lets a query for "did this
// request authenticate" filter on the field's presence rather than on it
// being a non-empty string.
func TestLogging_NoUserID_OmitsTheField(t *testing.T) {
	logger, buf := newTestLogger()

	handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))

	entry := decodeLogLine(t, buf)
	if _, present := entry["user_id"]; present {
		t.Errorf("logged entry has a user_id field for an unauthenticated request: %v", entry["user_id"])
	}
}

// TestRecordUserIDForLog_NoOpWithoutLogging covers the case
// RecordUserIDForLog's own doc comment promises: called against a plain
// context (no Logging in front, e.g. a test exercising RequireAuth in
// isolation), it must not panic and must simply do nothing observable.
func TestRecordUserIDForLog_NoOpWithoutLogging(t *testing.T) {
	RecordUserIDForLog(context.Background(), "user-1") // must not panic
}
