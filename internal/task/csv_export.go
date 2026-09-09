package task

import (
	"encoding/csv"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/JonasBorgesLM/task-api/internal/middleware"
)

// utf8BOM is written as the first three bytes of every CSV export.
// Without it, Excel on Windows — the primary consumer this format is
// aimed at — misreads non-ASCII bytes and mangles accented titles and
// descriptions, which this project's own content routinely has
// (Portuguese). The accepted cost, and why it was still chosen over
// leaving it out, is recorded in docs/DECISIONS.md § "BOM UTF-8 no
// export CSV".
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// csvExportHeader is the fixed column order for GET /tasks/export
// (issue #240). This is a public contract once published — a client
// that parses the export by position, not just by header name, breaks
// if a column is inserted or reordered. Add a new column at the end,
// never in the middle.
var csvExportHeader = []string{"id", "title", "description", "status", "priority", "created_at", "updated_at"}

// exportFilename builds the Content-Disposition filename for a CSV
// export: "tasks-<date>[-status-<v1-v2>][-priority-<v1-v2>].csv". The
// date and filter are both in the name because a file downloaded more
// than once, or compared against another day's export, needs its own
// scope legible without opening it — "tasks.csv" downloaded twice just
// overwrites itself silently in most browsers.
func exportFilename(now time.Time, statuses, priorities []string) string {
	name := "tasks-" + now.UTC().Format("2006-01-02")
	if len(statuses) > 0 {
		name += "-status-" + strings.Join(statuses, "-")
	}
	if len(priorities) > 0 {
		name += "-priority-" + strings.Join(priorities, "-")
	}
	return name + ".csv"
}

// csvFormulaTriggers are the leading characters/bytes that Excel and
// Google Sheets interpret as introducing a formula when a cell opens
// with one — see issue #241 (CWE-1236, CSV/formula injection). This is
// deliberately not applied anywhere near input validation
// (validateTitleAndDescription): inside the application these are
// ordinary legitimate text, and the risk exists only at the moment a
// value is written into a cell a spreadsheet will parse.
const csvFormulaTriggers = "=+-@\t\r"

// sanitizeCSVCell defuses formula injection by prefixing a leading
// single quote onto any field beginning with a character a spreadsheet
// would read as introducing a formula. Excel and Sheets both display
// the field as literal text with the quote hidden, rather than
// evaluating it — the standard mitigation for CWE-1236.
// TestExportTasks_Handler_NeutralizesFormulaInjection pins the exported
// behavior end to end; the guard itself was verified with a manual
// negative control (temporarily removing this call from exportTasks,
// confirming the same test then fails because the raw `=...` reaches
// the body, restoring it) rather than assumed correct from the diff.
func sanitizeCSVCell(field string) string {
	if field == "" {
		return field
	}
	if strings.ContainsRune(csvFormulaTriggers, rune(field[0])) {
		return "'" + field
	}
	return field
}

// exportTasks handles GET /tasks/export (issue #240) — streams every
// one of the caller's tasks matching the status/priority filter as
// RFC 4180 CSV, in the same order Repository.FindAll already applies.
// Never builds the response body in memory: each row is written and
// flushed to the connection as it's produced, so memory use stays
// proportional to one row, not to the result set's size (bounded
// anyway by Service.ExportTasks' maxExportRows check, which runs
// before this handler ever calls csv.Writer.Write).
func (h *Handler) exportTasks(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserIDFromContext(r.Context())
	query := r.URL.Query()
	statuses, priorities := query["status"], query["priority"]

	tasks, err := h.svc.ExportTasks(r.Context(), userID, statuses, priorities)
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	filename := exportFilename(time.Now(), statuses, priorities)
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	if disposition == "" {
		// FormatMediaType returns "" for a value it cannot encode. Every
		// character exportFilename can produce is already ASCII and
		// quote-free, so this is unreachable in practice; the fallback
		// matches internal/attachment/handler.go's download for the same
		// reason — losing the suggested filename is the acceptable half
		// to lose, never the download itself.
		disposition = "attachment"
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", disposition)
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(utf8BOM); err != nil {
		return // client gone; nothing left to report to it
	}

	cw := csv.NewWriter(w)
	cw.UseCRLF = true // RFC 4180 line terminator; encoding/csv defaults to bare "\n".

	flusher, canFlush := w.(http.Flusher)
	writeRow := func(fields []string) bool {
		if err := cw.Write(fields); err != nil {
			return false
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return false
		}
		if canFlush {
			flusher.Flush()
		}
		return true
	}

	if !writeRow(csvExportHeader) {
		return
	}
	for _, t := range tasks {
		row := []string{
			t.ID,
			sanitizeCSVCell(t.Title),
			sanitizeCSVCell(t.Description),
			string(t.Status),
			string(t.Priority),
			t.CreatedAt.UTC().Format(time.RFC3339),
			t.UpdatedAt.UTC().Format(time.RFC3339),
		}
		if !writeRow(row) {
			// The connection is gone (client disconnected, or a proxy
			// timed out it). The response is already committed with a
			// 200 and a partial body — there is no status left to
			// change, and no client left to send an error to.
			requestID, _ := middleware.RequestIDFromContext(r.Context())
			h.logger.Warn("csv export write failed mid-stream",
				"request_id", requestID,
			)
			return
		}
	}
}
