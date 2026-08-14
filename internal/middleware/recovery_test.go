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
