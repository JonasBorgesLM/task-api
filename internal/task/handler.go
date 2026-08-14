package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/JonasBorgesLM/task-api/internal/middleware"
)

// maxRequestBodyBytes caps the size of decoded JSON request bodies to guard
// against clients sending unbounded payloads.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// taskService is the interface the Handler depends on.
// It allows the Handler to be tested with a fake implementation.
type taskService interface {
	CreateTask(ctx context.Context, userID, title, description, priority string) (Task, error)
	GetTask(ctx context.Context, userID, id string) (Task, error)
	ListTasks(ctx context.Context, userID string, limit, offset int) ([]Task, error)
	UpdateTask(ctx context.Context, userID, id, title, description, priority string) (Task, error)
	DeleteTask(ctx context.Context, userID, id string) error
	CompleteTask(ctx context.Context, userID, id string) (Task, error)
	TransitionStatus(ctx context.Context, userID, id string, target Status) (Task, error)
}

// Handler exposes the task Service over HTTP.
type Handler struct {
	svc    taskService
	logger *slog.Logger
}

// NewHandler returns a new Handler with the given taskService and logger.
func NewHandler(svc taskService, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// RegisterRoutes registers all task routes on the given mux, each wrapped
// with requireAuth — every task route requires an authenticated caller,
// unlike user.Handler.RegisterRoutes, which only wraps some of its routes.
// requireAuth is what makes middleware.UserIDFromContext(r.Context())
// non-empty inside every handler method below.
func (h *Handler) RegisterRoutes(mux *http.ServeMux, requireAuth middleware.Middleware) {
	protect := func(hf http.HandlerFunc) http.Handler { return requireAuth(hf) }

	mux.Handle("POST /tasks", protect(h.createTask))
	mux.Handle("GET /tasks", protect(h.listTasks))
	mux.Handle("GET /tasks/{id}", protect(h.getTask))
	mux.Handle("PUT /tasks/{id}", protect(h.updateTask))
	mux.Handle("PATCH /tasks/{id}/done", protect(h.completeTask))
	mux.Handle("PATCH /tasks/{id}/status", protect(h.transitionStatus))
	mux.Handle("DELETE /tasks/{id}", protect(h.deleteTask))
}

// createTaskRequest is the accepted body for POST /tasks. Priority is
// optional — see Service.CreateTask for what an empty value defaults to.
type createTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
}

// updateTaskRequest is the accepted body for PUT /tasks/{id}. Priority is
// optional — an empty value leaves the task's current priority unchanged
// (see Service.UpdateTask).
type updateTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
}

// statusRequest is the accepted body for PATCH /tasks/{id}/status.
type statusRequest struct {
	Status string `json:"status"`
}

// createTask handles POST /tasks.
func (h *Handler) createTask(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	defer r.Body.Close()

	var req createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	userID, _ := middleware.UserIDFromContext(r.Context())

	task, err := h.svc.CreateTask(r.Context(), userID, req.Title, req.Description, req.Priority)
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	h.writeJSON(w, r, http.StatusCreated, task)
}

// listTasks handles GET /tasks. It accepts two optional query parameters,
// applied as a pagination window over Service's deterministic ordering
// (oldest first, ties broken by ID):
//
//   - limit: maximum number of tasks to return.
//   - offset: number of tasks to skip before collecting up to limit.
//
// Both default to "return everything" when absent, so existing callers
// that never pass them see no change in behavior. limit/offset are passed
// through to Service.ListTasks (and from there to Repository), rather than
// applied here after fetching every task — see Repository's doc comment
// for why that matters.
func (h *Handler) listTasks(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parsePagination(r.URL.Query())
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	userID, _ := middleware.UserIDFromContext(r.Context())

	tasks, err := h.svc.ListTasks(r.Context(), userID, limit, offset)
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	// Ensure an empty list serialises as [] and never as null.
	if tasks == nil {
		tasks = make([]Task, 0)
	}

	h.writeJSON(w, r, http.StatusOK, tasks)
}

// parsePagination reads the optional "limit" and "offset" query
// parameters. A missing limit is reported as -1 (paginate's "no limit"
// sentinel); a missing offset defaults to 0. Both, when present, must
// parse as non-negative integers.
func parsePagination(query url.Values) (limit, offset int, err error) {
	limit = -1

	if raw := query.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return 0, 0, fmt.Errorf("%w: limit must be a non-negative integer", ErrInvalidInput)
		}
	}

	if raw := query.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, fmt.Errorf("%w: offset must be a non-negative integer", ErrInvalidInput)
		}
	}

	return limit, offset, nil
}

// getTask handles GET /tasks/{id}.
func (h *Handler) getTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	userID, _ := middleware.UserIDFromContext(r.Context())

	task, err := h.svc.GetTask(r.Context(), userID, id)
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	h.writeJSON(w, r, http.StatusOK, task)
}

// updateTask handles PUT /tasks/{id}.
func (h *Handler) updateTask(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	defer r.Body.Close()

	id := r.PathValue("id")

	var req updateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	userID, _ := middleware.UserIDFromContext(r.Context())

	task, err := h.svc.UpdateTask(r.Context(), userID, id, req.Title, req.Description, req.Priority)
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	h.writeJSON(w, r, http.StatusOK, task)
}

// completeTask handles PATCH /tasks/{id}/done.
func (h *Handler) completeTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	userID, _ := middleware.UserIDFromContext(r.Context())

	task, err := h.svc.CompleteTask(r.Context(), userID, id)
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	h.writeJSON(w, r, http.StatusOK, task)
}

// transitionStatus handles PATCH /tasks/{id}/status.
func (h *Handler) transitionStatus(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	defer r.Body.Close()

	id := r.PathValue("id")

	var req statusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	userID, _ := middleware.UserIDFromContext(r.Context())

	task, err := h.svc.TransitionStatus(r.Context(), userID, id, Status(req.Status))
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	h.writeJSON(w, r, http.StatusOK, task)
}

// deleteTask handles DELETE /tasks/{id}.
func (h *Handler) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	userID, _ := middleware.UserIDFromContext(r.Context())

	if err := h.svc.DeleteTask(r.Context(), userID, id); err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleServiceError maps known domain errors to HTTP status codes.
//
// ErrNotFound, ErrInvalidInput and ErrAlreadyExists are expected, routine
// outcomes of a client's input — they are not logged here at all. The
// per-request access log line, written once by the Logging middleware,
// already records the resulting status (and therefore that something
// went wrong); logging them again here would just be the same failure
// recorded twice.
//
// Only a genuinely unexpected error — one Handler doesn't know how to map
// — is logged, and it is logged exactly once, at Error level, with the
// request ID, method and path attached so it can be correlated with the
// access log line for the same request.
func (h *Handler) handleServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		h.writeError(w, r, http.StatusNotFound, "task not found")
	case errors.Is(err, ErrInvalidInput):
		h.writeError(w, r, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrAlreadyExists):
		h.writeError(w, r, http.StatusConflict, "task already exists")
	case errors.Is(err, ErrConflict):
		h.writeError(w, r, http.StatusConflict, "task was modified concurrently, please retry")
	case errors.Is(err, ErrInvalidTransition):
		h.writeError(w, r, http.StatusConflict, err.Error())
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

// writeJSON sets Content-Type, writes the status code and encodes data as JSON.
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

// writeError writes a JSON error envelope: {"error": "message"}.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, status int, message string) {
	h.writeJSON(w, r, status, map[string]string{"error": message})
}
