package user

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/JonasBorgesLM/task-api/internal/middleware"
)

// maxRequestBodyBytes mirrors task/handler.go's cap on decoded JSON
// request bodies.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// userService is the interface Handler depends on, so it can be tested
// with a fake — the same pattern as task/handler.go's taskService.
type userService interface {
	Register(ctx context.Context, email, password string) (User, error)
	Authenticate(ctx context.Context, email, password string) (User, error)
	CreateSession(ctx context.Context, userID string) (token string, expiresAt time.Time, err error)
	Logout(ctx context.Context, token string) error
	LogoutAll(ctx context.Context, userID string) error
	GetUser(ctx context.Context, id string) (User, error)
}

// Handler exposes the user Service over HTTP.
type Handler struct {
	svc    userService
	logger *slog.Logger
}

// NewHandler returns a new Handler with the given userService and logger.
func NewHandler(svc userService, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// RegisterRoutes registers every /auth/* route on mux. Register and Login
// are public but rate-limited (see rateLimit — credential-stuffing and
// registration-spam targets, specifically); Logout and Me require an
// authenticated caller, enforced by wrapping those two handlers with
// requireAuth instead — the same per-route-wrapping approach
// task.Handler.RegisterRoutes uses for every one of its routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux, requireAuth, rateLimit middleware.Middleware) {
	mux.Handle("POST /auth/register", rateLimit(http.HandlerFunc(h.register)))
	mux.Handle("POST /auth/login", rateLimit(http.HandlerFunc(h.login)))
	mux.Handle("POST /auth/logout", requireAuth(http.HandlerFunc(h.logout)))
	mux.Handle("POST /auth/logout-all", requireAuth(http.HandlerFunc(h.logoutAll)))
	mux.Handle("GET /auth/me", requireAuth(http.HandlerFunc(h.me)))
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// register handles POST /auth/register.
func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	defer r.Body.Close()

	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	u, err := h.svc.Register(r.Context(), req.Email, req.Password)
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	h.writeJSON(w, r, http.StatusCreated, u)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// loginResponse is the body returned by a successful POST /auth/login.
// Token is a bearer credential: the client must send it back as
// "Authorization: Bearer <token>" on every subsequent request to a
// protected route.
type loginResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      User      `json:"user"`
}

// login handles POST /auth/login.
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	defer r.Body.Close()

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "invalid request body")
		return
	}

	u, err := h.svc.Authenticate(r.Context(), req.Email, req.Password)
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	token, expiresAt, err := h.svc.CreateSession(r.Context(), u.ID)
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	h.writeJSON(w, r, http.StatusOK, loginResponse{Token: token, ExpiresAt: expiresAt, User: u})
}

// logout handles POST /auth/logout. It reads the raw bearer token
// RequireAuth already stashed in the request context (see
// middleware.ContextWithSessionToken) rather than re-parsing the
// Authorization header — RequireAuth has already validated it once for
// this exact request.
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	token, _ := middleware.SessionTokenFromContext(r.Context())

	if err := h.svc.Logout(r.Context(), token); err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// logoutAll handles POST /auth/logout-all — deletes every session
// belonging to the authenticated caller, including the one that
// authenticated this very request. Meant for a user who suspects a
// token of theirs has leaked and wants every live session gone at once,
// rather than needing to know which one to target — see
// user.Service.LogoutAll's doc comment.
func (h *Handler) logoutAll(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserIDFromContext(r.Context())

	if err := h.svc.LogoutAll(r.Context(), userID); err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// me handles GET /auth/me — returns the authenticated caller's own User.
func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserIDFromContext(r.Context())

	u, err := h.svc.GetUser(r.Context(), userID)
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	h.writeJSON(w, r, http.StatusOK, u)
}

// handleServiceError maps known domain errors to HTTP status codes,
// mirroring task/handler.go's handleServiceError: routine, expected
// outcomes (bad input, unknown account, wrong credentials) are not logged
// here — the access log line already records the resulting status. Only a
// genuinely unexpected error is logged, once, at Error level.
func (h *Handler) handleServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		h.writeError(w, r, http.StatusNotFound, "user not found")
	case errors.Is(err, ErrInvalidInput):
		h.writeError(w, r, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrAlreadyExists):
		h.writeError(w, r, http.StatusConflict, "email already registered")
	case errors.Is(err, ErrInvalidCredentials):
		h.writeError(w, r, http.StatusUnauthorized, "invalid email or password")
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
