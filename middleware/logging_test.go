package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
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
