package user

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JonasBorgesLM/task-api/internal/middleware"
)

// newHandlerWithFakeAndLog is newHandlerWithFake, but the returned
// Handler writes JSON log lines into logBuf instead of discarding them —
// for tests asserting on logAuditEvent's output (issue #223).
func newHandlerWithFakeAndLog(svc *fakeService, logBuf *bytes.Buffer) *Handler {
	logger := slog.New(slog.NewJSONHandler(logBuf, nil))
	noopCascade := func(context.Context, string) error { return nil }
	return NewHandler(svc, logger, false, testCSRFProtector, false, noopCascade)
}

type auditLogLine struct {
	Msg       string `json:"msg"`
	EventType string `json:"event_type"`
	Account   string `json:"account"`
	SourceIP  string `json:"source_ip"`
	RequestID string `json:"request_id"`
}

// auditEvents decodes every JSON log line in logBuf and returns the ones
// whose message is "audit event" — the rest (if any) are other log
// lines this same Handler call happened to emit, not relevant here.
func auditEvents(t *testing.T, logBuf *bytes.Buffer) []auditLogLine {
	t.Helper()
	var events []auditLogLine
	scanner := bufio.NewScanner(logBuf)
	for scanner.Scan() {
		var line auditLogLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("failed to decode log line %q: %v", scanner.Text(), err)
		}
		if line.Msg == "audit event" {
			events = append(events, line)
		}
	}
	return events
}

func TestLogin_AuditEvent_OnSuccess(t *testing.T) {
	var logBuf bytes.Buffer
	svc := &fakeService{
		authenticateFn: func(email, _ string) (User, error) { return User{ID: "u1", Email: email}, nil },
	}
	h := newHandlerWithFakeAndLog(svc, &logBuf)

	do(h.login, http.MethodPost, "/auth/login", `{"email":"  User@Example.COM  ","password":"correct horse battery staple"}`)

	events := auditEvents(t, &logBuf)
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].EventType != auditEventLoginSuccess {
		t.Errorf("event_type = %q, want %q", events[0].EventType, auditEventLoginSuccess)
	}
	if events[0].Account != "user@example.com" {
		t.Errorf("account = %q, want normalized %q", events[0].Account, "user@example.com")
	}
}

func TestLogin_AuditEvent_OnWrongPassword(t *testing.T) {
	var logBuf bytes.Buffer
	svc := &fakeService{
		authenticateFn: func(_, _ string) (User, error) { return User{}, ErrInvalidCredentials },
	}
	h := newHandlerWithFakeAndLog(svc, &logBuf)

	do(h.login, http.MethodPost, "/auth/login", `{"email":"victim@example.com","password":"wrong"}`)

	events := auditEvents(t, &logBuf)
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].EventType != auditEventLoginFailure {
		t.Errorf("event_type = %q, want %q", events[0].EventType, auditEventLoginFailure)
	}
	if events[0].Account != "victim@example.com" {
		t.Errorf("account = %q, want %q", events[0].Account, "victim@example.com")
	}
}

// TestLogin_AuditEvent_OnUnknownEmail pins that a login_failure event
// fires for an unknown email exactly like it does for a wrong password —
// Service.Authenticate deliberately returns the same ErrInvalidCredentials
// for both (see its doc comment), and the audit trail must not
// accidentally depend on distinguishing them.
func TestLogin_AuditEvent_OnUnknownEmail(t *testing.T) {
	var logBuf bytes.Buffer
	svc := &fakeService{
		authenticateFn: func(_, _ string) (User, error) { return User{}, ErrInvalidCredentials },
	}
	h := newHandlerWithFakeAndLog(svc, &logBuf)

	do(h.login, http.MethodPost, "/auth/login", `{"email":"nobody@example.com","password":"whatever"}`)

	events := auditEvents(t, &logBuf)
	if len(events) != 1 || events[0].EventType != auditEventLoginFailure {
		t.Fatalf("audit events = %+v, want exactly one %q", events, auditEventLoginFailure)
	}
}

// TestLogin_AuditEvent_NotEmittedOnRepositoryError pins the scope
// boundary: a genuine infrastructure error is not a login attempt in the
// security-audit sense, and is already logged separately by
// handleServiceError's default branch — logAuditEvent must not also fire
// for it.
func TestLogin_AuditEvent_NotEmittedOnRepositoryError(t *testing.T) {
	var logBuf bytes.Buffer
	svc := &fakeService{
		authenticateFn: func(_, _ string) (User, error) { return User{}, errors.New("database down") },
	}
	h := newHandlerWithFakeAndLog(svc, &logBuf)

	do(h.login, http.MethodPost, "/auth/login", `{"email":"user@example.com","password":"whatever"}`)

	if events := auditEvents(t, &logBuf); len(events) != 0 {
		t.Errorf("audit events = %+v, want none for a repository error", events)
	}
}

func TestChangePassword_AuditEvent(t *testing.T) {
	var logBuf bytes.Buffer
	svc := &fakeService{}
	h := newHandlerWithFakeAndLog(svc, &logBuf)

	req := httptest.NewRequest(http.MethodPost, "/auth/password",
		strings.NewReader(`{"current_password":"old-password123","new_password":"new-password456"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), "u1"))
	req = req.WithContext(middleware.ContextWithSessionToken(req.Context(), "the-token"))
	w := httptest.NewRecorder()
	h.changePassword(w, req)

	events := auditEvents(t, &logBuf)
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].EventType != auditEventPasswordChanged {
		t.Errorf("event_type = %q, want %q", events[0].EventType, auditEventPasswordChanged)
	}
	if events[0].Account != "u1" {
		t.Errorf("account = %q, want %q", events[0].Account, "u1")
	}
}

func TestLogoutAll_AuditEvent(t *testing.T) {
	var logBuf bytes.Buffer
	svc := &fakeService{}
	h := newHandlerWithFakeAndLog(svc, &logBuf)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout-all", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), "u1"))
	w := httptest.NewRecorder()
	h.logoutAll(w, req)

	events := auditEvents(t, &logBuf)
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].EventType != auditEventLogoutAll {
		t.Errorf("event_type = %q, want %q", events[0].EventType, auditEventLogoutAll)
	}
	if events[0].Account != "u1" {
		t.Errorf("account = %q, want %q", events[0].Account, "u1")
	}
}

func TestDeleteAccount_AuditEvent(t *testing.T) {
	var logBuf bytes.Buffer
	svc := &fakeService{}
	h := newHandlerWithFakeAndLog(svc, &logBuf)

	req := httptest.NewRequest(http.MethodPost, "/auth/me",
		strings.NewReader(`{"current_password":"correct-password123"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), "u1"))
	w := httptest.NewRecorder()
	h.deleteAccount(w, req)

	events := auditEvents(t, &logBuf)
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].EventType != auditEventAccountDeleted {
		t.Errorf("event_type = %q, want %q", events[0].EventType, auditEventAccountDeleted)
	}
	if events[0].Account != "u1" {
		t.Errorf("account = %q, want %q", events[0].Account, "u1")
	}
}

// TestLogin_AuditEvent_CarriesSourceIPAndRequestID exercises the real
// middleware.RequestID/middleware.RealIP wiring end to end, unlike the
// other tests above (which call h.login directly and so see empty
// source_ip/request_id — RealIPFromContext/RequestIDFromContext degrade
// to "" when their middleware never ran, by design).
func TestLogin_AuditEvent_CarriesSourceIPAndRequestID(t *testing.T) {
	var logBuf bytes.Buffer
	svc := &fakeService{
		authenticateFn: func(email, _ string) (User, error) { return User{ID: "u1", Email: email}, nil },
	}
	h := newHandlerWithFakeAndLog(svc, &logBuf)

	fakeAddress := func(*http.Request) (string, error) { return "203.0.113.7", nil }
	wrapped := middleware.Chain(middleware.RequestID, middleware.RealIP(fakeAddress))(http.HandlerFunc(h.login))

	req := httptest.NewRequest(http.MethodPost, "/auth/login",
		strings.NewReader(`{"email":"user@example.com","password":"correct horse battery staple"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	events := auditEvents(t, &logBuf)
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1: %+v", len(events), events)
	}
	if events[0].SourceIP != "203.0.113.7" {
		t.Errorf("source_ip = %q, want %q", events[0].SourceIP, "203.0.113.7")
	}
	if events[0].RequestID == "" {
		t.Error("request_id = \"\", want a value assigned by middleware.RequestID")
	}
}
