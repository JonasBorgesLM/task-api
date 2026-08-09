package task

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// maxRequestBodyBytes caps the size of decoded JSON request bodies to guard
// against clients sending unbounded payloads.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// taskService is the interface the Handler depends on.
// It allows the Handler to be tested with a fake implementation.
type taskService interface {
	CreateTask(ctx context.Context, title, description string) (Task, error)
	GetTask(ctx context.Context, id string) (Task, error)
	ListTasks(ctx context.Context) ([]Task, error)
	UpdateTask(ctx context.Context, id, title, description string) (Task, error)
	DeleteTask(ctx context.Context, id string) error
	CompleteTask(ctx context.Context, id string) (Task, error)
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

// RegisterRoutes registers all task routes on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /tasks", h.createTask)
	mux.HandleFunc("GET /tasks", h.listTasks)
	mux.HandleFunc("GET /tasks/{id}", h.getTask)
	mux.HandleFunc("PUT /tasks/{id}", h.updateTask)
	mux.HandleFunc("PATCH /tasks/{id}/done", h.completeTask)
	mux.HandleFunc("DELETE /tasks/{id}", h.deleteTask)
}

// createTaskRequest is the accepted body for POST /tasks.
type createTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// updateTaskRequest is the accepted body for PUT /tasks/{id}.
type updateTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// createTask handles POST /tasks.
func (h *Handler) createTask(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	defer r.Body.Close()

	var req createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	task, err := h.svc.CreateTask(r.Context(), req.Title, req.Description)
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, task)
}

// listTasks handles GET /tasks.
func (h *Handler) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.svc.ListTasks(r.Context())
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	// Ensure an empty list serialises as [] and never as null.
	if tasks == nil {
		tasks = make([]Task, 0)
	}

	h.writeJSON(w, http.StatusOK, tasks)
}

// getTask handles GET /tasks/{id}.
func (h *Handler) getTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	task, err := h.svc.GetTask(r.Context(), id)
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, task)
}

// updateTask handles PUT /tasks/{id}.
func (h *Handler) updateTask(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	defer r.Body.Close()

	id := r.PathValue("id")

	var req updateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	task, err := h.svc.UpdateTask(r.Context(), id, req.Title, req.Description)
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, task)
}

// completeTask handles PATCH /tasks/{id}/done.
func (h *Handler) completeTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	task, err := h.svc.CompleteTask(r.Context(), id)
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, task)
}

// deleteTask handles DELETE /tasks/{id}.
func (h *Handler) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := h.svc.DeleteTask(r.Context(), id); err != nil {
		h.handleServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleServiceError maps known domain errors to HTTP status codes.
// Unexpected errors are logged at Error level and returned as 500.
func (h *Handler) handleServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		h.writeError(w, http.StatusNotFound, "task not found")
	case errors.Is(err, ErrInvalidInput):
		h.writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrAlreadyExists):
		h.writeError(w, http.StatusConflict, "task already exists")
	default:
		h.logger.Error("unexpected service error", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

// writeJSON sets Content-Type, writes the status code and encodes data as JSON.
func (h *Handler) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logger.Error("failed to encode response", "error", err)
	}
}

// writeError writes a JSON error envelope: {"error": "message"}.
func (h *Handler) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, map[string]string{"error": message})
}
