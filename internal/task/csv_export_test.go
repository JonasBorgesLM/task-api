package task

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// trimBOM strips a leading UTF-8 BOM (see utf8BOM) from a response body,
// so tests can parse the rest as ordinary CSV text.
func trimBOM(body []byte) []byte {
	return bytes.TrimPrefix(body, utf8BOM)
}

// --- GET /tasks/export ---

func TestExportTasks_Handler_ReturnsCSV(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tasks := []Task{
		{
			ID: "1", Title: "Buy milk", Description: "2%",
			Status: StatusPending, Priority: PriorityHigh,
			CreatedAt: created, UpdatedAt: created,
		},
	}
	svc := &fakeService{
		exportTasksFn: func(_ string, _, _ []string) ([]Task, error) { return tasks, nil },
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.exportTasks, http.MethodGet, "/tasks/export", "")

	if w.Code != http.StatusOK {
		t.Fatalf("exportTasks status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/csv; charset=utf-8")
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("Content-Disposition = %q, want it to start with %q", cd, "attachment;")
	}

	body := w.Body.Bytes()
	if !bytes.HasPrefix(body, utf8BOM) {
		t.Fatal("body does not start with a UTF-8 BOM")
	}
	body = trimBOM(body)

	if !strings.Contains(string(body), "\r\n") {
		t.Error("body has no CRLF line terminator, want RFC 4180's \\r\\n")
	}

	records, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatalf("failed to parse response body as CSV: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d CSV records (incl. header), want 2", len(records))
	}
	wantHeader := []string{"id", "title", "description", "status", "priority", "created_at", "updated_at"}
	if strings.Join(records[0], ",") != strings.Join(wantHeader, ",") {
		t.Errorf("header row = %v, want %v", records[0], wantHeader)
	}
	wantRow := []string{"1", "Buy milk", "2%", "pending", "high", "2026-01-02T03:04:05Z", "2026-01-02T03:04:05Z"}
	if strings.Join(records[1], ",") != strings.Join(wantRow, ",") {
		t.Errorf("data row = %v, want %v", records[1], wantRow)
	}
}

func TestExportTasks_Handler_EmptyResultIsHeaderOnly(t *testing.T) {
	svc := &fakeService{
		exportTasksFn: func(_ string, _, _ []string) ([]Task, error) { return []Task{}, nil },
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.exportTasks, http.MethodGet, "/tasks/export", "")

	body := trimBOM(w.Body.Bytes())
	records, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("failed to parse response body as CSV: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d CSV records, want 1 (header only)", len(records))
	}
}

// TestExportTasks_Handler_NeutralizesFormulaInjection is the end-to-end
// pin for issue #241 (CWE-1236): a title/description that a spreadsheet
// would read as opening a formula must reach the file with a leading
// single quote, not raw. sanitizeCSVCell's own guard was additionally
// verified with a manual negative control (temporarily removing the
// call from exportTasks and confirming this exact test then fails
// because the raw value comes through) before being restored.
func TestExportTasks_Handler_NeutralizesFormulaInjection(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  string
	}{
		{"equals", `=HYPERLINK("http://evil","click")`, `'=HYPERLINK("http://evil","click")`},
		{"plus", "+1+1", "'+1+1"},
		{"minus", "-1+1", "'-1+1"},
		{"at", "@SUM(1,1)", "'@SUM(1,1)"},
		{"tab", "\tcmd", "'\tcmd"},
		// Not "'\rcmd": encoding/csv.Writer, with UseCRLF set (RFC 4180's
		// line terminator, set in exportTasks), silently drops a bare \r
		// from field content — it assumes any \r it sees belongs to a
		// \r\n pair using the writer's own terminator convention.
		// Confirmed directly against the stdlib rather than assumed: the
		// leading single quote from sanitizeCSVCell still survives, so a
		// leading-CR title still can never reach the file as a raw
		// leading byte a spreadsheet could act on — the CR disappearing
		// entirely is a second, independent reason it's safe.
		{"carriage return", "\rcmd", "'cmd"},
		{"ordinary text is untouched", "Buy milk", "Buy milk"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{
				exportTasksFn: func(_ string, _, _ []string) ([]Task, error) {
					return []Task{{ID: "1", Title: tc.title, Status: StatusPending, Priority: PriorityLow}}, nil
				},
			}
			h := newHandlerWithFake(svc)

			w := do(t, h.exportTasks, http.MethodGet, "/tasks/export", "")

			body := trimBOM(w.Body.Bytes())
			records, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
			if err != nil {
				t.Fatalf("failed to parse response body as CSV: %v", err)
			}
			if len(records) != 2 {
				t.Fatalf("got %d records, want 2 (header + 1 row)", len(records))
			}
			if got := records[1][1]; got != tc.want {
				t.Errorf("title cell = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExportTasks_Handler_InvalidFilterIs400(t *testing.T) {
	svc := &fakeService{
		exportTasksFn: func(_ string, _, _ []string) ([]Task, error) {
			return nil, fmt.Errorf("%w: status must be one of pending, in_progress, done, cancelled", ErrInvalidInput)
		},
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.exportTasks, http.MethodGet, "/tasks/export?status=archived", "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("exportTasks with invalid status filter = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestExportTasks_Handler_TooManyRowsIs400 mirrors how
// Service.ExportTasks reports issue #243's cap being exceeded —
// ErrInvalidInput, the same as any other "change your request" 400.
func TestExportTasks_Handler_TooManyRowsIs400(t *testing.T) {
	svc := &fakeService{
		exportTasksFn: func(_ string, _, _ []string) ([]Task, error) {
			return nil, fmt.Errorf("%w: 20000 tasks match this filter, more than the 10000-row export limit; narrow the filter", ErrInvalidInput)
		},
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.exportTasks, http.MethodGet, "/tasks/export", "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("exportTasks over cap = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestExportTasks_Handler_RoutedCorrectly drives GET /tasks/export
// through the real http.ServeMux built by RegisterRoutes — not by
// calling h.exportTasks directly — to empirically confirm the literal
// segment "export" is never shadowed by the GET /tasks/{id} wildcard
// registered right after it, the same property
// TestTaskStats_Handler_RoutedCorrectly already pins for "stats".
func TestExportTasks_Handler_RoutedCorrectly(t *testing.T) {
	var routedToExport, routedToGetByID bool
	svc := &fakeService{
		exportTasksFn: func(_ string, _, _ []string) ([]Task, error) {
			routedToExport = true
			return []Task{}, nil
		},
		getTaskFn: func(_, _ string) (Task, error) {
			routedToGetByID = true
			return Task{}, ErrNotFound
		},
	}
	h := newHandlerWithFake(svc)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, passthroughAuth)

	req := httptest.NewRequest(http.MethodGet, "/tasks/export", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /tasks/export status = %d, want %d", w.Code, http.StatusOK)
	}
	if !routedToExport {
		t.Error("GET /tasks/export did not reach exportTasks")
	}
	if routedToGetByID {
		t.Error("GET /tasks/export was routed to getTask instead of exportTasks — \"export\" was treated as a task id")
	}
}

// --- exportFilename ---

func TestExportFilename(t *testing.T) {
	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		statuses   []string
		priorities []string
		want       string
	}{
		{"no filter", nil, nil, "tasks-2026-03-05.csv"},
		{"status only", []string{"pending", "done"}, nil, "tasks-2026-03-05-status-pending-done.csv"},
		{"priority only", nil, []string{"high"}, "tasks-2026-03-05-priority-high.csv"},
		{"both", []string{"pending"}, []string{"high", "medium"}, "tasks-2026-03-05-status-pending-priority-high-medium.csv"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exportFilename(now, tc.statuses, tc.priorities); got != tc.want {
				t.Errorf("exportFilename() = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- sanitizeCSVCell ---

func TestSanitizeCSVCell(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty string is untouched", "", ""},
		{"ordinary text is untouched", "Buy milk", "Buy milk"},
		{"leading equals is prefixed", "=1+1", "'=1+1"},
		{"leading plus is prefixed", "+1", "'+1"},
		{"leading minus is prefixed", "-1", "'-1"},
		{"leading at is prefixed", "@cmd", "'@cmd"},
		{"leading tab is prefixed", "\tcmd", "'\tcmd"},
		{"leading CR is prefixed", "\rcmd", "'\rcmd"},
		{"trigger character mid-string is untouched", "a=b", "a=b"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeCSVCell(tc.input); got != tc.want {
				t.Errorf("sanitizeCSVCell(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
