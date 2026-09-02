package user

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JonasBorgesLM/task-api/internal/middleware"
)

// protectedHandler is the handler RequireAuth wraps in these tests. It
// records whether it was reached at all, and what RequireAuth put in the
// request context when it was.
type protectedHandler struct {
	called      bool
	gotUserID   string
	gotToken    string
	userIDOK    bool
	sessionOK   bool
	statusToSet int
}

func (p *protectedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.called = true
	p.gotUserID, p.userIDOK = middleware.UserIDFromContext(r.Context())
	p.gotToken, p.sessionOK = middleware.SessionTokenFromContext(r.Context())
	if p.statusToSet != 0 {
		w.WriteHeader(p.statusToSet)
	}
}

// serveWithAuth runs one request through RequireAuth(svc) wrapping a
// protectedHandler, returning the response, the handler, and whatever the
// middleware logged.
func serveWithAuth(t *testing.T, svc sessionValidator, authHeader string) (*httptest.ResponseRecorder, *protectedHandler, *bytes.Buffer) {
	t.Helper()
	return serveWithCredentials(t, svc, authHeader, "")
}

// serveWithCredentials is serveWithAuth generalized to also attach a
// session cookie, for the credentialSource precedence/cookie tests below.
// cookieValue == "" attaches no cookie at all — see
// TestRequireAuth_CookiePresentButEmpty_TreatedAsAbsent for the distinct
// case of a cookie that is present with an empty value.
func serveWithCredentials(t *testing.T, svc sessionValidator, authHeader, cookieValue string) (*httptest.ResponseRecorder, *protectedHandler, *bytes.Buffer) {
	t.Helper()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	next := &protectedHandler{}
	handler := RequireAuth(svc, logger)(next)

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if cookieValue != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookieValue})
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	return w, next, &logBuf
}

func decodeErrorBody(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body %q: %v", w.Body.String(), err)
	}
	return body["error"]
}

// --- Rejected before any session lookup ---

func TestRequireAuth_MissingHeader_Returns401(t *testing.T) {
	w, next, _ := serveWithAuth(t, &fakeService{}, "")

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if next.called {
		t.Error("wrapped handler must not be reached without an Authorization header")
	}
}

func TestRequireAuth_MalformedHeader_Returns401(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"wrong scheme", "Basic dXNlcjpwYXNz"},
		{"bearer with no token", "Bearer "},
		{"raw token without scheme", "sometoken"},
		{"lowercase scheme", "bearer sometoken"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, next, _ := serveWithAuth(t, &fakeService{}, tc.header)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
			}
			if next.called {
				t.Error("wrapped handler must not be reached for a malformed header")
			}
		})
	}
}

// --- 401: the credential really is bad ---

// TestRequireAuth_UnknownOrExpiredToken_Returns401 covers the one case
// where 401 is the correct answer: ValidateToken reports ErrNotFound,
// meaning the token is genuinely unknown or has expired. Nothing is
// logged — this is a routine outcome of client input, and the access log
// line already records the resulting status.
func TestRequireAuth_UnknownOrExpiredToken_Returns401(t *testing.T) {
	svc := &fakeService{
		validateTokenFn: func(string) (string, error) { return "", ErrNotFound },
	}

	w, next, logBuf := serveWithAuth(t, svc, "Bearer sometoken")

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if next.called {
		t.Error("wrapped handler must not be reached for an invalid token")
	}
	if got := decodeErrorBody(t, w); got != "invalid or expired session token" {
		t.Errorf("error = %q, want %q", got, "invalid or expired session token")
	}
	if logBuf.Len() != 0 {
		t.Errorf("an expired/unknown token must not be logged, got: %q", logBuf.String())
	}
}

// TestRequireAuth_WrappedErrNotFound_Returns401 guards the errors.Is
// unwrapping specifically: Service.ValidateToken wraps the repository's
// ErrNotFound ("validate token: %w"), so a plain equality check would
// miss it and misclassify a genuinely-invalid token as a 503 outage.
func TestRequireAuth_WrappedErrNotFound_Returns401(t *testing.T) {
	svc := &fakeService{
		validateTokenFn: func(string) (string, error) {
			return "", errors.New("validate token: " + ErrNotFound.Error())
		},
	}
	// Sanity: a string-formatted error is NOT ErrNotFound, so this must be
	// a 503 — the point of the test below is the properly-wrapped case.
	w, _, _ := serveWithAuth(t, svc, "Bearer sometoken")
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("unwrappable error: status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	wrapped := &fakeService{
		validateTokenFn: func(string) (string, error) {
			return "", fmt.Errorf("validate token: %w", ErrNotFound)
		},
	}
	w2, _, logBuf := serveWithAuth(t, wrapped, "Bearer sometoken")
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("wrapped ErrNotFound: status = %d, want %d", w2.Code, http.StatusUnauthorized)
	}
	if logBuf.Len() != 0 {
		t.Errorf("wrapped ErrNotFound must not be logged, got: %q", logBuf.String())
	}
}

// --- 503: the lookup failed, which says nothing about the credential ---

// TestRequireAuth_LookupFailure_Returns503AndLogs is the regression guard
// for the defect this middleware previously had: a database outage made
// ValidateToken fail for *every* token, valid ones included, and the
// middleware answered 401 "invalid or expired session token". That told
// clients to throw away working credentials, and — because 401 is logged
// at WARN — hid a total outage from any 5xx-based alerting.
func TestRequireAuth_LookupFailure_Returns503AndLogs(t *testing.T) {
	dbErr := errors.New("failed to connect to `user=task_api database=task_api`: connection refused")
	svc := &fakeService{
		validateTokenFn: func(string) (string, error) { return "", dbErr },
	}

	w, next, logBuf := serveWithAuth(t, svc, "Bearer a-perfectly-valid-token")

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d — an infrastructure failure must not be reported as a bad credential", w.Code, http.StatusServiceUnavailable)
	}
	if next.called {
		t.Error("wrapped handler must not be reached when the session lookup failed")
	}

	if got := decodeErrorBody(t, w); got == "invalid or expired session token" {
		t.Error("a 503 must not tell the client its token is invalid")
	}

	// The cause must be logged exactly once, at ERROR, so an operator can
	// actually diagnose the outage.
	if logBuf.Len() == 0 {
		t.Fatal("a session lookup failure must be logged")
	}
	var entry map[string]any
	if err := json.Unmarshal(logBuf.Bytes(), &entry); err != nil {
		t.Fatalf("decode log line %q: %v", logBuf.String(), err)
	}
	if entry["level"] != "ERROR" {
		t.Errorf("log level = %v, want ERROR", entry["level"])
	}
	if got, _ := entry["error"].(string); !strings.Contains(got, "connection refused") {
		t.Errorf("logged error = %v, want it to carry the underlying cause", entry["error"])
	}
	if entry["path"] != "/tasks" || entry["method"] != http.MethodGet {
		t.Errorf("log is missing method/path context: %v", entry)
	}
}

// TestRequireAuth_LookupFailure_IsNot401 states the invariant plainly for
// a few distinct infrastructure failures: none of them may produce a 401.
func TestRequireAuth_LookupFailure_IsNot401(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"connection refused", errors.New("dial tcp: connection refused")},
		{"query timeout", errors.New("context deadline exceeded")},
		{"canceled context", errors.New("context canceled")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{
				validateTokenFn: func(string) (string, error) { return "", tc.err },
			}

			w, _, _ := serveWithAuth(t, svc, "Bearer sometoken")

			if w.Code == http.StatusUnauthorized {
				t.Errorf("%s produced 401; an infrastructure failure must never be reported as a credential problem", tc.name)
			}
			if w.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
			}
		})
	}
}

// --- Success ---

func TestRequireAuth_ValidToken_PopulatesContext(t *testing.T) {
	const (
		wantUserID = "11111111-1111-4111-8111-111111111111"
		wantToken  = "a-valid-token"
	)
	svc := &fakeService{
		validateTokenFn: func(token string) (string, error) {
			if token != wantToken {
				t.Errorf("ValidateToken got token %q, want %q", token, wantToken)
			}
			return wantUserID, nil
		},
	}

	w, next, logBuf := serveWithAuth(t, svc, "Bearer "+wantToken)

	if !next.called {
		t.Fatal("wrapped handler was not reached for a valid token")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !next.userIDOK || next.gotUserID != wantUserID {
		t.Errorf("user ID in context = %q (ok=%v), want %q", next.gotUserID, next.userIDOK, wantUserID)
	}
	if !next.sessionOK || next.gotToken != wantToken {
		t.Errorf("session token in context = %q (ok=%v), want %q", next.gotToken, next.sessionOK, wantToken)
	}
	if logBuf.Len() != 0 {
		t.Errorf("a successful auth must not be logged, got: %q", logBuf.String())
	}
}

// --- credentialSource: cookie, and precedence against Authorization ---
//
// See docs/DECISIONS.md § "Autenticação: modo duplo (cookie httpOnly +
// Bearer)" for why the cookie exists at all, and CI-3 of
// docs/changes/dual-auth-mode/plan.md for why Authorization wins when
// both are present.

func TestRequireAuth_CookieOnly_Authenticates(t *testing.T) {
	const (
		wantUserID = "11111111-1111-4111-8111-111111111111"
		wantToken  = "a-valid-cookie-token"
	)
	svc := &fakeService{
		validateTokenFn: func(token string) (string, error) {
			if token != wantToken {
				t.Errorf("ValidateToken got token %q, want %q", token, wantToken)
			}
			return wantUserID, nil
		},
	}

	w, next, logBuf := serveWithCredentials(t, svc, "", wantToken)

	if !next.called {
		t.Fatal("wrapped handler was not reached for a valid cookie")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !next.userIDOK || next.gotUserID != wantUserID {
		t.Errorf("user ID in context = %q (ok=%v), want %q", next.gotUserID, next.userIDOK, wantUserID)
	}
	if !next.sessionOK || next.gotToken != wantToken {
		t.Errorf("session token in context = %q (ok=%v), want %q", next.gotToken, next.sessionOK, wantToken)
	}
	if logBuf.Len() != 0 {
		t.Errorf("a successful auth must not be logged, got: %q", logBuf.String())
	}
}

func TestRequireAuth_CookieUnknownOrExpired_Returns401(t *testing.T) {
	// The same ErrNotFound handling Bearer already gets — the credential
	// source must not change how a bad token is reported.
	svc := &fakeService{
		validateTokenFn: func(string) (string, error) { return "", ErrNotFound },
	}

	w, next, _ := serveWithCredentials(t, svc, "", "some-cookie-token")

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if next.called {
		t.Error("wrapped handler must not be reached for an invalid cookie token")
	}
}

// TestRequireAuth_HeaderAndCookieBothPresent_HeaderWins is the
// precedence rule CI-3 introduces, tested explicitly rather than argued:
// Authorization is what actually reaches ValidateToken, not the cookie,
// when a request somehow carries both.
func TestRequireAuth_HeaderAndCookieBothPresent_HeaderWins(t *testing.T) {
	const (
		headerToken = "header-token"
		cookieToken = "cookie-token"
	)
	var gotToken string
	svc := &fakeService{
		validateTokenFn: func(token string) (string, error) {
			gotToken = token
			return "some-user-id", nil
		},
	}

	w, next, _ := serveWithCredentials(t, svc, "Bearer "+headerToken, cookieToken)

	if !next.called {
		t.Fatal("wrapped handler was not reached")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if gotToken != headerToken {
		t.Errorf("ValidateToken called with %q, want the header token %q — Authorization must win over the cookie", gotToken, headerToken)
	}
	if next.gotToken != headerToken {
		t.Errorf("session token in context = %q, want the header token %q", next.gotToken, headerToken)
	}
}

// TestRequireAuth_CookiePresentButEmpty_TreatedAsAbsent and
// TestRequireAuth_GarbledCookieHeader_TreatedAsAbsent pin "cookie
// malformado/vazio tratado como ausente (não gera 500)" from CI-3's plan
// — neither reaches ValidateToken at all, the same way a missing
// Authorization header never did.
func TestRequireAuth_CookiePresentButEmpty_TreatedAsAbsent(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	next := &protectedHandler{}
	handler := RequireAuth(&fakeService{}, logger)(next)

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: ""})

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if next.called {
		t.Error("wrapped handler must not be reached with an empty cookie value")
	}
}

func TestRequireAuth_GarbledCookieHeader_TreatedAsAbsent(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	next := &protectedHandler{}
	handler := RequireAuth(&fakeService{}, logger)(next)

	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Cookie", ";;;not a valid cookie header;;;")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d — a garbled Cookie header must not panic or 500", w.Code, http.StatusUnauthorized)
	}
	if next.called {
		t.Error("wrapped handler must not be reached with a garbled Cookie header")
	}
}
