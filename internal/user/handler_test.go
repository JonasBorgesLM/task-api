package user

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JonasBorgesLM/moat/csrf"
	"github.com/JonasBorgesLM/moat/secret"

	"github.com/JonasBorgesLM/task-api/internal/middleware"
)

// testCSRFSecret is a fixed, valid-length secret for tests that need a
// real csrf.Protector rather than a pass-through — csrf.New rejects
// anything shorter than csrf.MinSecretLen (32 bytes), so this is padded
// well past it.
const testCSRFSecret = "test-only-csrf-secret-not-for-production-use-000000"

// newTestCSRFProtector returns a real *csrf.Protector for tests, with
// WithInsecureCookie so it works in a plain httptest request (no TLS).
func newTestCSRFProtector(t *testing.T) *csrf.Protector {
	t.Helper()
	p, err := csrf.New(secret.New([]byte(testCSRFSecret)), csrf.WithInsecureCookie())
	if err != nil {
		t.Fatalf("csrf.New() unexpected error: %v", err)
	}
	return p
}

// fakeService is a test double for userService (Handler) and
// sessionValidator (RequireAuth) — one fake implements both, since
// RequireAuth only needs ValidateToken.
type fakeService struct {
	registerFn      func(email, password string) (User, error)
	authenticateFn  func(email, password string) (User, error)
	createSessionFn func(userID string) (string, time.Time, error)
	logoutFn        func(token string) error
	logoutAllFn     func(userID string) error
	getUserFn       func(id string) (User, error)
	validateTokenFn func(token string) (string, error)

	logoutCalledWith    string
	logoutAllCalledWith string
	getUserCalledWith   string
}

func (f *fakeService) Register(_ context.Context, email, password string) (User, error) {
	if f.registerFn != nil {
		return f.registerFn(email, password)
	}
	return User{}, nil
}

func (f *fakeService) Authenticate(_ context.Context, email, password string) (User, error) {
	if f.authenticateFn != nil {
		return f.authenticateFn(email, password)
	}
	return User{}, nil
}

func (f *fakeService) CreateSession(_ context.Context, userID string) (string, time.Time, error) {
	if f.createSessionFn != nil {
		return f.createSessionFn(userID)
	}
	return "token", time.Now().Add(time.Hour), nil
}

func (f *fakeService) Logout(_ context.Context, token string) error {
	f.logoutCalledWith = token
	if f.logoutFn != nil {
		return f.logoutFn(token)
	}
	return nil
}

func (f *fakeService) LogoutAll(_ context.Context, userID string) error {
	f.logoutAllCalledWith = userID
	if f.logoutAllFn != nil {
		return f.logoutAllFn(userID)
	}
	return nil
}

func (f *fakeService) GetUser(_ context.Context, id string) (User, error) {
	f.getUserCalledWith = id
	if f.getUserFn != nil {
		return f.getUserFn(id)
	}
	return User{}, nil
}

func (f *fakeService) ValidateToken(_ context.Context, token string) (string, error) {
	if f.validateTokenFn != nil {
		return f.validateTokenFn(token)
	}
	return "", ErrNotFound
}

func newHandlerWithFake(svc *fakeService) *Handler {
	return newHandlerWithFakeCookieMode(svc, false)
}

// newHandlerWithFakeCookieMode is newHandlerWithFake with cookieInsecure
// exposed, for the tests that specifically exercise its effect on the
// session cookie's Secure attribute.
func newHandlerWithFakeCookieMode(svc *fakeService, cookieInsecure bool) *Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandler(svc, logger, cookieInsecure)
}

func do(handler http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.NewDecoder(w.Body).Decode(dst); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
}

// --- POST /auth/register ---

func TestRegister_Handler_ValidJSON(t *testing.T) {
	svc := &fakeService{
		registerFn: func(email, password string) (User, error) {
			return User{ID: "u1", Email: email}, nil
		},
	}
	h := newHandlerWithFake(svc)

	w := do(h.register, http.MethodPost, "/auth/register", `{"email":"user@example.com","password":"password123"}`)

	if w.Code != http.StatusCreated {
		t.Errorf("register status = %d, want %d", w.Code, http.StatusCreated)
	}

	var got User
	decodeBody(t, w, &got)
	if got.Email != "user@example.com" {
		t.Errorf("register body Email = %q, want %q", got.Email, "user@example.com")
	}
}

func TestRegister_Handler_InvalidJSON(t *testing.T) {
	h := newHandlerWithFake(&fakeService{})

	w := do(h.register, http.MethodPost, "/auth/register", `{invalid}`)

	if w.Code != http.StatusBadRequest {
		t.Errorf("register invalid JSON status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestRegister_Handler_DuplicateEmail(t *testing.T) {
	svc := &fakeService{
		registerFn: func(_, _ string) (User, error) { return User{}, ErrAlreadyExists },
	}
	h := newHandlerWithFake(svc)

	w := do(h.register, http.MethodPost, "/auth/register", `{"email":"user@example.com","password":"password123"}`)

	if w.Code != http.StatusConflict {
		t.Errorf("register duplicate email status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestRegister_Handler_InvalidInput(t *testing.T) {
	svc := &fakeService{
		registerFn: func(_, _ string) (User, error) { return User{}, ErrInvalidInput },
	}
	h := newHandlerWithFake(svc)

	w := do(h.register, http.MethodPost, "/auth/register", `{"email":"","password":""}`)

	if w.Code != http.StatusBadRequest {
		t.Errorf("register invalid input status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// --- POST /auth/login ---

func TestLogin_Handler_ValidCredentials(t *testing.T) {
	svc := &fakeService{
		authenticateFn: func(email, _ string) (User, error) { return User{ID: "u1", Email: email}, nil },
		createSessionFn: func(userID string) (string, time.Time, error) {
			return "a-real-token", time.Now().Add(time.Hour), nil
		},
	}
	h := newHandlerWithFake(svc)

	w := do(h.login, http.MethodPost, "/auth/login", `{"email":"user@example.com","password":"password123"}`)

	if w.Code != http.StatusOK {
		t.Errorf("login status = %d, want %d", w.Code, http.StatusOK)
	}

	var got loginResponse
	decodeBody(t, w, &got)
	if got.Token != "a-real-token" {
		t.Errorf("login body Token = %q, want %q", got.Token, "a-real-token")
	}
	if got.User.Email != "user@example.com" {
		t.Errorf("login body User.Email = %q, want %q", got.User.Email, "user@example.com")
	}
}

// TestLogin_Handler_SetsSessionCookie is CI-4's core assertion: the four
// attributes docs/DECISIONS.md § "Autenticação: modo duplo" commits to,
// on the same response TestLogin_Handler_ValidCredentials already proves
// carries an unchanged body — this is the "the cookie is additive, not a
// replacement" half of that guarantee, not a substitute for it.
func TestLogin_Handler_SetsSessionCookie(t *testing.T) {
	expiresAt := time.Now().Add(2 * time.Hour)
	svc := &fakeService{
		authenticateFn: func(email, _ string) (User, error) { return User{ID: "u1", Email: email}, nil },
		createSessionFn: func(userID string) (string, time.Time, error) {
			return "a-real-token", expiresAt, nil
		},
	}
	h := newHandlerWithFake(svc)

	w := do(h.login, http.MethodPost, "/auth/login", `{"email":"user@example.com","password":"password123"}`)

	cookie := findCookie(t, w, sessionCookieName)
	if cookie.Value != "a-real-token" {
		t.Errorf("cookie Value = %q, want %q — must carry the same token the body does", cookie.Value, "a-real-token")
	}
	if !cookie.HttpOnly {
		t.Error("cookie HttpOnly = false, want true")
	}
	if !cookie.Secure {
		t.Error("cookie Secure = false, want true (cookieInsecure is false in this test)")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want SameSiteLaxMode", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("cookie Path = %q, want %q", cookie.Path, "/")
	}
	// Allow a couple of seconds of slack for the time between expiresAt
	// being computed above and the cookie being written.
	if cookie.MaxAge < 7195 || cookie.MaxAge > 7200 {
		t.Errorf("cookie MaxAge = %d, want close to 7200 (2h)", cookie.MaxAge)
	}
}

func TestLogin_Handler_CookieInsecure_DropsSecureAttribute(t *testing.T) {
	svc := &fakeService{
		authenticateFn: func(email, _ string) (User, error) { return User{ID: "u1", Email: email}, nil },
	}
	h := newHandlerWithFakeCookieMode(svc, true)

	w := do(h.login, http.MethodPost, "/auth/login", `{"email":"user@example.com","password":"password123"}`)

	cookie := findCookie(t, w, sessionCookieName)
	if cookie.Secure {
		t.Error("cookie Secure = true, want false — cookieInsecure(true) must drop it for http://localhost in dev")
	}
	if !cookie.HttpOnly {
		t.Error("cookieInsecure must only affect Secure, not HttpOnly")
	}
}

// findCookie decodes every Set-Cookie header on w and returns the one
// named name, failing the test if none matches.
func findCookie(t *testing.T, w *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no Set-Cookie named %q in response; got %v", name, w.Result().Header.Values("Set-Cookie"))
	return nil
}

func TestLogin_Handler_InvalidCredentials(t *testing.T) {
	svc := &fakeService{
		authenticateFn: func(_, _ string) (User, error) { return User{}, ErrInvalidCredentials },
	}
	h := newHandlerWithFake(svc)

	w := do(h.login, http.MethodPost, "/auth/login", `{"email":"user@example.com","password":"wrong"}`)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("login invalid credentials status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestLogin_Handler_InvalidJSON(t *testing.T) {
	h := newHandlerWithFake(&fakeService{})

	w := do(h.login, http.MethodPost, "/auth/login", `{invalid}`)

	if w.Code != http.StatusBadRequest {
		t.Errorf("login invalid JSON status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// --- POST /auth/logout ---

func TestLogout_Handler_UsesTokenFromContext(t *testing.T) {
	svc := &fakeService{}
	h := newHandlerWithFake(svc)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req = req.WithContext(middleware.ContextWithSessionToken(req.Context(), "the-token"))
	w := httptest.NewRecorder()
	h.logout(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("logout status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if svc.logoutCalledWith != "the-token" {
		t.Errorf("logout called Service.Logout with %q, want %q", svc.logoutCalledWith, "the-token")
	}
}

// TestLogout_Handler_ExpiresSessionCookie is the wire-level half of "the
// server invalidates the session, and so does the browser stop sending
// its cookie" — MaxAge < 0 is what net/http turns into the literal
// "Max-Age=0" a browser deletes a cookie on sight for (see
// clearSessionCookie's doc comment; MaxAge == 0, the zero value, would
// instead omit the attribute and leave the original cookie's expiry
// untouched).
func TestLogout_Handler_ExpiresSessionCookie(t *testing.T) {
	svc := &fakeService{}
	h := newHandlerWithFake(svc)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req = req.WithContext(middleware.ContextWithSessionToken(req.Context(), "the-token"))
	w := httptest.NewRecorder()
	h.logout(w, req)

	cookie := findCookie(t, w, sessionCookieName)
	if cookie.MaxAge >= 0 {
		t.Errorf("cookie MaxAge = %d, want negative (net/http's spelling for immediate deletion)", cookie.MaxAge)
	}
	if cookie.Path != "/" {
		t.Errorf("cookie Path = %q, want %q — must match what setSessionCookie wrote or the browser keeps the original", cookie.Path, "/")
	}
}

// --- POST /auth/logout-all ---

func TestLogoutAll_Handler_UsesUserIDFromContext(t *testing.T) {
	svc := &fakeService{}
	h := newHandlerWithFake(svc)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout-all", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), "u1"))
	w := httptest.NewRecorder()
	h.logoutAll(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("logout-all status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if svc.logoutAllCalledWith != "u1" {
		t.Errorf("logout-all called Service.LogoutAll with %q, want %q", svc.logoutAllCalledWith, "u1")
	}
}

// TestLogoutAll_Handler_ExpiresSessionCookie mirrors
// TestLogout_Handler_ExpiresSessionCookie — logout-all ends every
// session, including the one that made this very request, so the
// browser must stop sending its cookie exactly the same way.
func TestLogoutAll_Handler_ExpiresSessionCookie(t *testing.T) {
	svc := &fakeService{}
	h := newHandlerWithFake(svc)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout-all", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), "u1"))
	w := httptest.NewRecorder()
	h.logoutAll(w, req)

	cookie := findCookie(t, w, sessionCookieName)
	if cookie.MaxAge >= 0 {
		t.Errorf("cookie MaxAge = %d, want negative (net/http's spelling for immediate deletion)", cookie.MaxAge)
	}
}

func TestLogoutAll_Handler_RepositoryError(t *testing.T) {
	svc := &fakeService{
		logoutAllFn: func(_ string) error { return errors.New("boom") },
	}
	h := newHandlerWithFake(svc)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout-all", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), "u1"))
	w := httptest.NewRecorder()
	h.logoutAll(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("logout-all with a repository error: status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// --- GET /auth/me ---

func TestMe_Handler_UsesUserIDFromContext(t *testing.T) {
	svc := &fakeService{
		getUserFn: func(id string) (User, error) { return User{ID: id, Email: "user@example.com"}, nil },
	}
	h := newHandlerWithFake(svc)

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), "u1"))
	w := httptest.NewRecorder()
	h.me(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("me status = %d, want %d", w.Code, http.StatusOK)
	}
	if svc.getUserCalledWith != "u1" {
		t.Errorf("me called Service.GetUser with %q, want %q", svc.getUserCalledWith, "u1")
	}

	var got User
	decodeBody(t, w, &got)
	if got.ID != "u1" {
		t.Errorf("me body ID = %q, want %q", got.ID, "u1")
	}
}

// --- GET /auth/csrf-token ---

// TestCSRFToken_Handler_ReturnsToken drives the request through a real
// csrf.Protector.Middleware first, the same way cmd/api's newServer will
// (see CI-6 of docs/changes/dual-auth-mode/plan.md) — csrf.Token only
// finds a value once that has happened, so testing h.csrfToken in
// isolation would prove nothing about the actual guarantee.
func TestCSRFToken_Handler_ReturnsToken(t *testing.T) {
	h := newHandlerWithFake(&fakeService{})
	protector := newTestCSRFProtector(t)

	req := httptest.NewRequest(http.MethodGet, "/auth/csrf-token", nil)
	w := httptest.NewRecorder()
	protector.Middleware(http.HandlerFunc(h.csrfToken)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("csrf-token status = %d, want %d", w.Code, http.StatusOK)
	}

	var got csrfTokenResponse
	decodeBody(t, w, &got)
	if got.CSRFToken == "" {
		t.Error("csrf-token body csrf_token is empty, want a real token")
	}
}

// TestCSRFToken_Handler_MiddlewareNotWired_Returns500 is the wiring-bug
// case csrfToken's own doc comment describes: called directly, without
// csrf.Protector.Middleware in front of it, csrf.Token reports ok=false
// and the handler must fail loudly (500, logged) rather than silently
// returning an empty token that would make every subsequent write fail
// CSRF with no clue why.
func TestCSRFToken_Handler_MiddlewareNotWired_Returns500(t *testing.T) {
	h := newHandlerWithFake(&fakeService{})

	req := httptest.NewRequest(http.MethodGet, "/auth/csrf-token", nil)
	w := httptest.NewRecorder()
	h.csrfToken(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("csrf-token without middleware: status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// --- RegisterRoutes ---

func TestRegisterRoutes_PublicAndProtectedRoutes(t *testing.T) {
	svc := &fakeService{
		registerFn:      func(email, _ string) (User, error) { return User{Email: email}, nil },
		authenticateFn:  func(email, _ string) (User, error) { return User{Email: email}, nil },
		createSessionFn: func(_ string) (string, time.Time, error) { return "tok", time.Now().Add(time.Hour), nil },
		getUserFn:       func(id string) (User, error) { return User{ID: id}, nil },
		validateTokenFn: func(token string) (string, error) {
			if token == "valid-token" {
				return "u1", nil
			}
			return "", ErrNotFound
		},
	}
	h := newHandlerWithFake(svc)
	mux := http.NewServeMux()
	// A generous limit here: this test exercises route wiring, not
	// RateLimiter itself (see internal/middleware/rate_limit_test.go for
	// that).
	// A pass-through: these tests cover routing and auth, not rate
	// limiting, and the limiter that guards these routes in production is
	// composed in cmd/api (see newServer) rather than here.
	noopRateLimit := func(next http.Handler) http.Handler { return next }
	// A real Protector, not a pass-through: "csrf-token is public" below
	// asserts on the actual response body, which only exists once a
	// request has gone through csrf.Protector.Middleware for real.
	csrfProtector := newTestCSRFProtector(t)
	h.RegisterRoutes(mux, RequireAuth(svc, slog.New(slog.NewTextHandler(io.Discard, nil))), noopRateLimit, csrfProtector.Middleware)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		token  string
		want   int
	}{
		{"register is public", http.MethodPost, "/auth/register", `{"email":"a@example.com","password":"password123"}`, "", http.StatusCreated},
		{"login is public", http.MethodPost, "/auth/login", `{"email":"a@example.com","password":"password123"}`, "", http.StatusOK},
		{"csrf-token is public", http.MethodGet, "/auth/csrf-token", "", "", http.StatusOK},
		{"logout without token is rejected", http.MethodPost, "/auth/logout", "", "", http.StatusUnauthorized},
		{"logout with valid token succeeds", http.MethodPost, "/auth/logout", "", "valid-token", http.StatusNoContent},
		{"me without token is rejected", http.MethodGet, "/auth/me", "", "", http.StatusUnauthorized},
		{"me with valid token succeeds", http.MethodGet, "/auth/me", "", "valid-token", http.StatusOK},
		{"me with invalid token is rejected", http.MethodGet, "/auth/me", "", "not-a-real-token", http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("%s %s status = %d, want %d", tc.method, tc.path, w.Code, tc.want)
			}
		})
	}
}

// --- Error mapping ---

func TestHandler_UnexpectedError_Returns500(t *testing.T) {
	svc := &fakeService{
		getUserFn: func(_ string) (User, error) { return User{}, errors.New("database exploded") },
	}
	h := newHandlerWithFake(svc)

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), "u1"))
	w := httptest.NewRecorder()
	h.me(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("unexpected error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	var body map[string]string
	decodeBody(t, w, &body)
	if strings.Contains(body["error"], "database") {
		t.Error("500 response must not expose internal error details")
	}
}
