package attachment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"

	"github.com/JonasBorgesLM/task-api/internal/middleware"
)

// multipartMemoryBytes is how much of a multipart body mime/multipart
// keeps in memory before spilling the remainder to a temporary file. It
// is not a size limit — that is Service's maxBytes, enforced while
// streaming to the store — only a memory/IO trade-off for the parse
// itself.
const multipartMemoryBytes = 1 << 20 // 1 MiB

// fileFormField is the multipart field name an upload must use. It is
// fixed rather than discovered: accepting whichever field happens to
// carry a file means a request with several of them has no defined
// meaning.
const fileFormField = "file"

// attachmentService is the interface Handler depends on, so it can be
// tested with a fake.
type attachmentService interface {
	Upload(ctx context.Context, userID, taskID, declaredFilename string, r io.Reader) (Attachment, error)
	Download(ctx context.Context, userID, storageKey string) (Attachment, io.ReadSeekCloser, error)
	ListByTask(ctx context.Context, userID, taskID string) ([]Attachment, error)
	Delete(ctx context.Context, userID, storageKey string) error
}

// Handler exposes the attachment Service over HTTP.
type Handler struct {
	svc      attachmentService
	logger   *slog.Logger
	maxBytes int64
}

// NewHandler returns a Handler. maxBytes must match the limit the Service
// was built with: it is used here only to bound the request body before
// parsing, so an oversized upload is refused without being buffered.
func NewHandler(svc attachmentService, logger *slog.Logger, maxBytes int64) *Handler {
	return &Handler{svc: svc, logger: logger, maxBytes: maxBytes}
}

// RegisterRoutes registers the attachment routes, every one of them
// behind requireAuth.
//
// GET /files/{key} is deliberately not nested under /tasks: a download
// addresses a stored blob by its own key, and the task it hangs off is
// something the server resolves rather than something the client asserts.
// A path like /tasks/{id}/attachments/{key} would invite exactly the
// confused-deputy shape where the two disagree and one of them is
// believed.
func (h *Handler) RegisterRoutes(mux *http.ServeMux, requireAuth middleware.Middleware) {
	protect := func(hf http.HandlerFunc) http.Handler { return requireAuth(hf) }

	mux.Handle("POST /tasks/{id}/attachments", protect(h.upload))
	mux.Handle("GET /tasks/{id}/attachments", protect(h.list))
	mux.Handle("GET /files/{key}", protect(h.download))
	mux.Handle("DELETE /files/{key}", protect(h.delete))
}

// upload handles POST /tasks/{id}/attachments.
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		// Unreachable behind requireAuth. Refusing rather than
		// continuing with an empty user ID means a wiring mistake shows
		// up as a failure instead of as an unscoped write.
		h.writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Bound the body before the parser sees it. MaxBytesReader is what
	// makes an oversized upload cost the server a rejection rather than
	// the whole file; Service's own limit still applies underneath, and
	// this one is deliberately looser to leave room for the multipart
	// framing around the file itself.
	r.Body = http.MaxBytesReader(w, r.Body, h.maxBytes+multipartMemoryBytes)

	reader, err := r.MultipartReader()
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "request body must be multipart/form-data")
		return
	}

	// Streaming the parts, rather than ParseMultipartForm, so the file
	// never lands in a temporary file on its way to the store.
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			h.writeError(w, r, http.StatusBadRequest, "malformed multipart body")
			return
		}

		if part.FormName() != fileFormField {
			part.Close()
			continue
		}

		att, err := h.svc.Upload(r.Context(), userID, r.PathValue("id"), part.FileName(), part)
		part.Close()
		if err != nil {
			h.handleServiceError(w, r, err)
			return
		}

		h.writeJSON(w, r, http.StatusCreated, att)
		return
	}

	h.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("multipart body must contain a %q part", fileFormField))
}

// list handles GET /tasks/{id}/attachments.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		h.writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}

	attachments, err := h.svc.ListByTask(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	if attachments == nil {
		attachments = make([]Attachment, 0)
	}

	h.writeJSON(w, r, http.StatusOK, attachments)
}

// delete handles DELETE /files/{key}. Same path shape as download,
// deliberately: see RegisterRoutes' doc comment on why a stored blob is
// addressed by its own key rather than nested under /tasks/{id}.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		h.writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}

	if err := h.svc.Delete(r.Context(), userID, r.PathValue("key")); err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// download handles GET /files/{key}.
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserIDFromContext(r.Context())
	if !ok {
		h.writeError(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}

	att, blob, err := h.svc.Download(r.Context(), userID, r.PathValue("key"))
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}
	defer blob.Close()

	// Content-Disposition: attachment is load-bearing, not a nicety.
	// These bytes came from a user and are served from this API's own
	// origin, so anything a browser agreed to render here would run as
	// same-origin. "attachment" tells it to download rather than
	// display, and mime.FormatMediaType encodes the filename so that a
	// name containing a quote or a non-ASCII character cannot break out
	// of the header value.
	//
	// It works together with X-Content-Type-Options: nosniff (set for
	// every response by secureheaders) — without that, a browser may
	// disregard the declared type and decide for itself what the bytes
	// are.
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": att.OriginalFilename})
	if disposition == "" {
		// FormatMediaType returns "" for a value it cannot encode.
		// Falling back to a bare directive keeps the download safe;
		// losing the suggested filename is the acceptable half to lose.
		disposition = "attachment"
	}

	w.Header().Set("Content-Type", att.ContentType)
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Length", strconv.FormatInt(att.SizeBytes, 10))

	// ServeContent rather than io.Copy: it handles Range requests and
	// conditional headers, which is what makes a large download
	// resumable. The modtime is the attachment's creation time — the
	// bytes are immutable once stored, so that is genuinely when this
	// content last changed.
	http.ServeContent(w, r, att.OriginalFilename, att.CreatedAt, blob)
}

// handleServiceError maps domain errors to HTTP status codes. It is the
// only place in this package that does so, and it must never grow a
// branch that knows what backs the Repository or the BlobStore.
func (h *Handler) handleServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		h.writeError(w, r, http.StatusNotFound, "attachment not found")
	case errors.Is(err, ErrTaskNotFound):
		h.writeError(w, r, http.StatusNotFound, "task not found")
	case errors.Is(err, ErrInvalidInput):
		h.writeError(w, r, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrAlreadyExists):
		// A collision between two server-generated UUIDs is not
		// something the caller did or can fix by changing the request,
		// so this is a server fault rather than a 409.
		requestID, _ := middleware.RequestIDFromContext(r.Context())
		h.logger.Error("attachment identifier collision",
			"error", err,
			"request_id", requestID,
			"path", r.URL.Path,
		)
		h.writeError(w, r, http.StatusInternalServerError, "internal server error")
	default:
		requestID, _ := middleware.RequestIDFromContext(r.Context())
		h.logger.Error("unexpected service error",
			"error", err,
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
		)
		h.writeError(w, r, http.StatusInternalServerError, "internal server error")
	}
}

func (h *Handler) writeJSON(w http.ResponseWriter, r *http.Request, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		requestID, _ := middleware.RequestIDFromContext(r.Context())
		h.logger.Error("failed to encode response",
			"error", err,
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
		)
	}
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, status int, message string) {
	h.writeJSON(w, r, status, map[string]string{"error": message})
}
