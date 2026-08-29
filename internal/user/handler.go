package user

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/JonasBorgesLM/moat/csrf"

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
	svc            userService
	logger         *slog.Logger
	cookieInsecure bool
	csrfProtector  *csrf.Protector
}

// NewHandler returns a new Handler with the given userService and logger.
// cookieInsecure mirrors config.Config.CookieInsecure — see its doc
// comment for why the session cookie ever drops Secure at all (only for
// http://localhost in local development; never in production).
// csrfProtector is the same *csrf.Protector cmd/api's newServer builds
// and wires into the global CSRF gate (internal/middleware/csrf.go) —
// login uses it to rotate the CSRF cookie on every successful
// authentication (see login's doc comment).
func NewHandler(svc userService, logger *slog.Logger, cookieInsecure bool, csrfProtector *csrf.Protector) *Handler {
	return &Handler{svc: svc, logger: logger, cookieInsecure: cookieInsecure, csrfProtector: csrfProtector}
}

// RegisterRoutes registers every /auth/* route on mux. Register and Login
// are public but rate-limited (see rateLimit — credential-stuffing and
// registration-spam targets, specifically); Logout and Me require an
// authenticated caller, enforced by wrapping those two handlers with
// requireAuth instead — the same per-route-wrapping approach
// task.Handler.RegisterRoutes uses for every one of its routes.
//
// GET /auth/csrf-token is public and otherwise unwrapped here — a caller
// has no session the first time it needs this token (see
// docs/DECISIONS.md § "Autenticação: modo duplo (cookie httpOnly +
// Bearer)": it is required on POST /auth/login and /auth/register too,
// not just on already-authenticated routes). It still needs to pass
// through csrf.Protector.Middleware for csrf.Token to have anything to
// read, but that is cmd/api's job now: middleware.CSRF is mounted on the
// global chain in newServer (see internal/middleware/csrf.go), covering
// every safe method — including this route — before routing ever
// happens. An earlier version of this method wrapped this one route with
// its own csrfMiddleware parameter; that became redundant once the
// global gate existed and was removed rather than left as a second,
// needless pass through the same Protector.
func (h *Handler) RegisterRoutes(mux *http.ServeMux, requireAuth, rateLimit middleware.Middleware) {
	mux.Handle("POST /auth/register", rateLimit(http.HandlerFunc(h.register)))
	mux.Handle("POST /auth/login", rateLimit(http.HandlerFunc(h.login)))
	mux.Handle("GET /auth/csrf-token", http.HandlerFunc(h.csrfToken))
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

	// Rotate replaces the CSRF cookie's value, so a token computed
	// against whatever value it held before this login (e.g. one an
	// attacker planted, the fixation csrf.Protector.Rotate's own doc
	// comment describes) no longer matches after it. Must run here,
	// before anything is written to w — Rotate itself detects and
	// reports a late call rather than silently doing nothing, but
	// nothing later in this handler should ever trigger that. Best
	// effort: a failure here would mean the outstanding CSRF fixation
	// window isn't closed for this login, not that the login itself
	// failed, so it is logged rather than turned into a 500 — the
	// session cookie login is actually here to issue is unaffected.
	if _, err := h.csrfProtector.Rotate(w, r); err != nil {
		requestID, _ := middleware.RequestIDFromContext(r.Context())
		h.logger.Error("csrf cookie rotation failed on login",
			"error", err,
			"request_id", requestID,
		)
	}

	token, expiresAt, err := h.svc.CreateSession(r.Context(), u.ID)
	if err != nil {
		h.handleServiceError(w, r, err)
		return
	}

	// Must happen before writeJSON: Set-Cookie is a header, and headers
	// can no longer be added once the body starts writing. See
	// docs/DECISIONS.md § "Autenticação: modo duplo (cookie httpOnly +
	// Bearer)" — the cookie carries the same token as the body's own
	// "token" field, which is unchanged by this: a client that only ever
	// read the body keeps working exactly as before.
	setSessionCookie(w, token, expiresAt, h.cookieInsecure)

	h.writeJSON(w, r, http.StatusOK, loginResponse{Token: token, ExpiresAt: expiresAt, User: u})
}

// setSessionCookie writes the session cookie a browser client
// authenticates with from then on, carrying the same opaque token
// loginResponse.Token already does. HttpOnly and the deliberate absence
// of any client-readable duplicate are what make it immune to theft via
// XSS, unlike the localStorage alternative docs/DECISIONS.md rejected.
//
// Its expiry mirrors the token's own expiresAt exactly, rather than a
// separately-configured duration: the cookie must never outlive, or
// undercut, the credential it carries.
func setSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time, insecure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionCookieMaxAgeSeconds(expiresAt),
		HttpOnly: true,
		Secure:   !insecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie expires the session cookie on logout/logout-all.
//
// MaxAge: -1 is Go's spelling for "delete this cookie now" — net/http
// writes that as the wire value "Max-Age=0" (the attribute meaning
// "already expired"), which is different from the Go zero value
// (MaxAge: 0), which omits the Max-Age attribute entirely and would
// leave the cookie's original expiry in place. Every other attribute
// must match what setSessionCookie wrote, or the browser treats this as
// a different cookie and leaves the real one untouched.
func clearSessionCookie(w http.ResponseWriter, insecure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   !insecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// sessionCookieMaxAgeSeconds is setSessionCookie's Max-Age, in whole
// seconds until expiresAt. CreateSession always returns a time in the
// future, so this is never zero or negative in practice — but the value
// is used as-is rather than clamped, so a bug upstream would surface as
// a cookie the browser discards immediately, not as one silently valid
// for longer than the token actually is.
func sessionCookieMaxAgeSeconds(expiresAt time.Time) int {
	return int(time.Until(expiresAt).Seconds())
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

	clearSessionCookie(w, h.cookieInsecure)
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

	clearSessionCookie(w, h.cookieInsecure)
	w.WriteHeader(http.StatusNoContent)
}

// csrfTokenResponse is the body returned by GET /auth/csrf-token.
type csrfTokenResponse struct {
	CSRFToken string `json:"csrf_token"`
}

// csrfToken handles GET /auth/csrf-token — see RegisterRoutes' doc
// comment for why it is public.
//
// The token comes from csrf.Token(r), which only finds one if this
// request already passed through csrf.Protector.Middleware — that
// wiring is RegisterRoutes' csrfMiddleware parameter, supplied by
// cmd/api's newServer, not anything this method constructs itself. A
// false ok here is therefore a wiring bug, not a client error — this
// route somehow bypassed the middleware that is supposed to cover every
// request — so it is logged and reported as 500, the same treatment
// handleServiceError's default branch gives any other unexpected
// failure. Returning an empty token instead would turn a wiring mistake
// into every subsequent write failing CSRF with no clue why — exactly
// what csrf.Token's own doc comment warns a bare-string signature would
// invite.
func (h *Handler) csrfToken(w http.ResponseWriter, r *http.Request) {
	token, ok := csrf.Token(r)
	if !ok {
		requestID, _ := middleware.RequestIDFromContext(r.Context())
		h.logger.Error("csrf token requested but csrf.Protector.Middleware never ran for this request",
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
		)
		h.writeError(w, r, http.StatusInternalServerError, "internal server error")
		return
	}

	h.writeJSON(w, r, http.StatusOK, csrfTokenResponse{CSRFToken: token})
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
