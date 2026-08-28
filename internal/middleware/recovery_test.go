package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecovery_CatchesPanicAndReturns500(t *testing.T) {
	logger, _ := newTestLogger()

	handler := Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Recovery() status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestRecovery_DoesNotLeakPanicValueOrStackToClient(t *testing.T) {
	logger, _ := newTestLogger()

	handler := Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("internal detail: db password hunter2")
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	body := w.Body.String()
	if strings.Contains(body, "hunter2") {
		t.Errorf("response body leaked the panic value: %q", body)
	}
	if strings.Contains(body, "goroutine") {
		t.Errorf("response body leaked a stack trace: %q", body)
	}

	var payload map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if payload["error"] != "internal server error" {
		t.Errorf("response body error = %q, want a generic message", payload["error"])
	}
}

func TestRecovery_LogsPanicInternally(t *testing.T) {
	logger, buf := newTestLogger()

	handler := Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	logged := buf.String()
	if !strings.Contains(logged, "boom") {
		t.Errorf("panic value was not logged internally: %q", logged)
	}
	if !strings.Contains(logged, "panic recovered") {
		t.Errorf("expected a %q log message, got: %q", "panic recovered", logged)
	}
}

func TestRecovery_NoPanic_PassesThrough(t *testing.T) {
	logger, buf := newTestLogger()

	handler := Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	if w.Body.String() != `{"ok":true}` {
		t.Errorf("body = %q, want %q", w.Body.String(), `{"ok":true}`)
	}
	if buf.Len() != 0 {
		t.Errorf("Recovery() logged something for a non-panicking request: %q", buf.String())
	}
}

func TestRecovery_LogsRequestIDWhenPresent(t *testing.T) {
	logger, buf := newTestLogger()

	handler := RequestID(Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	wantID := w.Header().Get(HeaderRequestID)

	entry := decodeLogLine(t, buf)
	if entry["request_id"] != wantID {
		t.Errorf("logged request_id = %v, want %v", entry["request_id"], wantID)
	}
}

func TestRecovery_RePanicsOnErrAbortHandler(t *testing.T) {
	logger, _ := newTestLogger()

	handler := Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		got := recover()
		if got != http.ErrAbortHandler {
			t.Errorf("recovered value = %v, want http.ErrAbortHandler", got)
		}
	}()

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	t.Fatal("expected ServeHTTP to panic with http.ErrAbortHandler, but it returned normally")
}

// TestRecovery_ResponseAlreadyStarted_DoesNotWriteASecondOne is the test
// the removed always-write behavior would have failed: a handler that
// wrote a 200 (via Flush, so it actually reaches the wire rather than
// sitting buffered) and then panicked used to still get Recovery's
// WriteHeader(500) on top of it — a "superfluous response.WriteHeader"
// from net/http and a second, invalid body concatenated onto the first.
//
// Recovery only ever sees this because it is wrapped inside Logging in
// production (cmd/api/main.go): Logging's statusRecorder is what
// observes and reports the write. This test reproduces that composition
// rather than calling Recovery alone, because the property being tested
// belongs to the pair, not to Recovery in isolation.
func TestRecovery_ResponseAlreadyStarted_DoesNotWriteASecondOne(t *testing.T) {
	logger, buf := newTestLogger()

	handler := Logging(logger)(Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		panic("boom after the response already started")
	})))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	// The original 200 stands — Recovery must not have overwritten it.
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (the response already committed before the panic)",
			w.Code, http.StatusOK)
	}
	// No error JSON was appended to the body.
	if w.Body.Len() != 0 {
		t.Errorf("body = %q, want empty — Recovery must not write after the response started", w.Body.String())
	}

	logged := buf.String()
	if !strings.Contains(logged, "boom after the response already started") {
		t.Errorf("panic value was not logged internally: %q", logged)
	}
	if !strings.Contains(logged, `"response_already_started":true`) {
		t.Errorf("expected response_already_started=true in the log line: %q", logged)
	}
}

// TestRecovery_ResponseNotYetStarted_StillWrites500 pins the ordinary
// case next to the one above: when Logging wraps Recovery (the
// production composition) and the handler panics before writing
// anything, Recovery still writes its 500 exactly as before — the new
// check changes behavior only when a response genuinely already started.
func TestRecovery_ResponseNotYetStarted_StillWrites500(t *testing.T) {
	logger, buf := newTestLogger()

	handler := Logging(logger)(Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom before anything was written")
	})))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	var payload map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if payload["error"] != "internal server error" {
		t.Errorf("response body error = %q, want a generic message", payload["error"])
	}

	if !strings.Contains(buf.String(), `"response_already_started":false`) {
		t.Errorf("expected response_already_started=false in the log line: %q", buf.String())
	}
}
