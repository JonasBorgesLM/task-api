package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cairn"

	"github.com/JonasBorgesLM/task-api/internal/middleware"
)

const testUserID = "11111111-1111-1111-1111-111111111111"

// fakeService is a test double for linkService.
type fakeService struct {
	createFn func(ownerID string, req CreateRequest) (*cairn.Link, error)
	listFn   func(ownerID string, after cairn.Cursor, limit int) ([]*cairn.Link, cairn.Cursor, error)
	revokeFn func(ownerID string, code cairn.Code) error

	createCalledWith [2]any // [ownerID, req]
	listCalledWith   [3]any // [ownerID, after, limit]
	revokeCalledWith [2]any // [ownerID, code]
}

func (f *fakeService) Create(_ context.Context, ownerID string, req CreateRequest) (*cairn.Link, error) {
	f.createCalledWith = [2]any{ownerID, req}
	if f.createFn != nil {
		return f.createFn(ownerID, req)
	}
	return &cairn.Link{}, nil
}

func (f *fakeService) List(_ context.Context, ownerID string, after cairn.Cursor, limit int) ([]*cairn.Link, cairn.Cursor, error) {
	f.listCalledWith = [3]any{ownerID, after, limit}
	if f.listFn != nil {
		return f.listFn(ownerID, after, limit)
	}
	return nil, "", nil
}

func (f *fakeService) Revoke(_ context.Context, ownerID string, code cairn.Code) error {
	f.revokeCalledWith = [2]any{ownerID, code}
	if f.revokeFn != nil {
		return f.revokeFn(ownerID, code)
	}
	return nil
}

func newHandlerWithFake(svc *fakeService) *Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandler(svc, logger, "https://s.example.com")
}

func passthroughAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := middleware.ContextWithUserID(r.Context(), testUserID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func do(t *testing.T, handler http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), testUserID))
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

func sampleLink() *cairn.Link {
	dest, err := cairn.ParseDestination("https://example.com/a")
	if err != nil {
		panic(err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &cairn.Link{
		Code:      "abc123",
		Dest:      dest,
		OwnerID:   testUserID,
		CreatedAt: now,
	}
}

// --- POST /links ---

func TestCreateLink_Handler_ValidJSON(t *testing.T) {
	link := sampleLink()
	svc := &fakeService{createFn: func(_ string, _ CreateRequest) (*cairn.Link, error) { return link, nil }}
	h := newHandlerWithFake(svc)

	w := do(t, h.createLink, http.MethodPost, "/links", `{"url":"https://example.com/a"}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("createLink status = %d, want %d", w.Code, http.StatusCreated)
	}
	var got linkResponse
	decodeBody(t, w, &got)
	if got.Code != "abc123" {
		t.Errorf("createLink body Code = %q, want %q", got.Code, "abc123")
	}
	if got.ShortURL != "https://s.example.com/abc123" {
		t.Errorf("createLink body ShortURL = %q, want %q", got.ShortURL, "https://s.example.com/abc123")
	}
	if got.URL != "https://example.com/a" {
		t.Errorf("createLink body URL = %q, want %q", got.URL, "https://example.com/a")
	}
	if svc.createCalledWith[0] != testUserID {
		t.Errorf("createLink called Service.Create with ownerID = %v, want %q", svc.createCalledWith[0], testUserID)
	}
}

func TestCreateLink_Handler_InvalidJSON(t *testing.T) {
	h := newHandlerWithFake(&fakeService{})

	w := do(t, h.createLink, http.MethodPost, "/links", `{`)

	if w.Code != http.StatusBadRequest {
		t.Errorf("createLink invalid JSON status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateLink_Handler_DestinationRejected(t *testing.T) {
	svc := &fakeService{
		createFn: func(_ string, _ CreateRequest) (*cairn.Link, error) {
			return nil, cairn.NewRejectionError(cairn.ReasonPrivateAddress)
		},
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.createLink, http.MethodPost, "/links", `{"url":"https://127.0.0.1/"}`)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("createLink rejected destination status = %d, want %d", w.Code, http.StatusUnprocessableEntity)
	}
	var got errorResponse
	decodeBody(t, w, &got)
	if got.Reason != string(cairn.ReasonPrivateAddress) {
		t.Errorf("createLink body Reason = %q, want %q", got.Reason, cairn.ReasonPrivateAddress)
	}
}

func TestCreateLink_Handler_CodeExists(t *testing.T) {
	svc := &fakeService{createFn: func(_ string, _ CreateRequest) (*cairn.Link, error) { return nil, cairn.ErrCodeExists }}
	h := newHandlerWithFake(svc)

	w := do(t, h.createLink, http.MethodPost, "/links", `{"url":"https://example.com/a","vanity_code":"taken"}`)

	if w.Code != http.StatusConflict {
		t.Errorf("createLink code-exists status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestCreateLink_Handler_StoreUnavailable(t *testing.T) {
	svc := &fakeService{createFn: func(_ string, _ CreateRequest) (*cairn.Link, error) { return nil, cairn.ErrStoreUnavailable }}
	h := newHandlerWithFake(svc)

	w := do(t, h.createLink, http.MethodPost, "/links", `{"url":"https://example.com/a"}`)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("createLink store-unavailable status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

// --- GET /links ---

func TestListLinks_Handler_ReturnsOK(t *testing.T) {
	link := sampleLink()
	svc := &fakeService{
		listFn: func(_ string, _ cairn.Cursor, _ int) ([]*cairn.Link, cairn.Cursor, error) {
			return []*cairn.Link{link}, "", nil
		},
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.listLinks, http.MethodGet, "/links", "")

	if w.Code != http.StatusOK {
		t.Fatalf("listLinks status = %d, want %d", w.Code, http.StatusOK)
	}
	var got []linkResponse
	decodeBody(t, w, &got)
	if len(got) != 1 || got[0].Code != "abc123" {
		t.Errorf("listLinks body = %+v, want one link with code abc123", got)
	}
	if w.Header().Get("X-Next-Cursor") != "" {
		t.Errorf("X-Next-Cursor = %q, want empty (no further page)", w.Header().Get("X-Next-Cursor"))
	}
}

func TestListLinks_Handler_EmptyIsArray(t *testing.T) {
	h := newHandlerWithFake(&fakeService{listFn: func(_ string, _ cairn.Cursor, _ int) ([]*cairn.Link, cairn.Cursor, error) {
		return nil, "", nil
	}})

	w := do(t, h.listLinks, http.MethodGet, "/links", "")

	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("listLinks empty body = %q, want %q", w.Body.String(), "[]")
	}
}

func TestListLinks_Handler_SetsNextCursorHeader(t *testing.T) {
	svc := &fakeService{
		listFn: func(_ string, _ cairn.Cursor, _ int) ([]*cairn.Link, cairn.Cursor, error) {
			return []*cairn.Link{sampleLink()}, cairn.Cursor("20"), nil
		},
	}
	h := newHandlerWithFake(svc)

	w := do(t, h.listLinks, http.MethodGet, "/links", "")

	if got := w.Header().Get("X-Next-Cursor"); got != "20" {
		t.Errorf("X-Next-Cursor = %q, want %q", got, "20")
	}
}

func TestListLinks_Handler_DefaultAndCustomLimit(t *testing.T) {
	svc := &fakeService{}
	h := newHandlerWithFake(svc)

	do(t, h.listLinks, http.MethodGet, "/links", "")
	if svc.listCalledWith[2] != defaultListLimit {
		t.Errorf("listLinks default limit = %v, want %d", svc.listCalledWith[2], defaultListLimit)
	}

	do(t, h.listLinks, http.MethodGet, "/links?limit=5", "")
	if svc.listCalledWith[2] != 5 {
		t.Errorf("listLinks custom limit = %v, want 5", svc.listCalledWith[2])
	}
}

func TestListLinks_Handler_PassesAfterCursor(t *testing.T) {
	svc := &fakeService{}
	h := newHandlerWithFake(svc)

	do(t, h.listLinks, http.MethodGet, "/links?after=20", "")

	if svc.listCalledWith[1] != cairn.Cursor("20") {
		t.Errorf("listLinks after = %v, want %q", svc.listCalledWith[1], "20")
	}
}

func TestListLinks_Handler_InvalidLimit(t *testing.T) {
	cases := []string{"?limit=0", "?limit=-1", "?limit=abc"}
	for _, qs := range cases {
		t.Run(qs, func(t *testing.T) {
			h := newHandlerWithFake(&fakeService{})
			w := do(t, h.listLinks, http.MethodGet, "/links"+qs, "")
			if w.Code != http.StatusBadRequest {
				t.Errorf("listLinks %s status = %d, want %d", qs, w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestListLinks_Handler_LimitAboveMaxIs400(t *testing.T) {
	h := newHandlerWithFake(&fakeService{})

	w := do(t, h.listLinks, http.MethodGet, fmt.Sprintf("/links?limit=%d", maxListLimit+1), "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("listLinks over-max limit status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// --- DELETE /links/{code} ---

func TestRevokeLink_Handler_ReturnsNoContent(t *testing.T) {
	svc := &fakeService{}
	h := newHandlerWithFake(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /links/{code}", h.revokeLink)

	req := httptest.NewRequest(http.MethodDelete, "/links/abc123", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), testUserID))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("revokeLink status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if svc.revokeCalledWith[1] != cairn.Code("abc123") {
		t.Errorf("revokeLink called Service.Revoke with code = %v, want %q", svc.revokeCalledWith[1], "abc123")
	}
}

func TestRevokeLink_Handler_NotOwner_Returns404(t *testing.T) {
	svc := &fakeService{revokeFn: func(_ string, _ cairn.Code) error { return ErrNotOwner }}
	h := newHandlerWithFake(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /links/{code}", h.revokeLink)

	req := httptest.NewRequest(http.MethodDelete, "/links/abc123", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), testUserID))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("revokeLink wrong-owner status = %d, want %d (never 403 — see .claude/rules/go-domain-errors.md)", w.Code, http.StatusNotFound)
	}
}

func TestRevokeLink_Handler_NotFound_Returns404(t *testing.T) {
	svc := &fakeService{revokeFn: func(_ string, _ cairn.Code) error { return cairn.ErrCodeNotFound }}
	h := newHandlerWithFake(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /links/{code}", h.revokeLink)

	req := httptest.NewRequest(http.MethodDelete, "/links/nope", nil)
	req = req.WithContext(middleware.ContextWithUserID(req.Context(), testUserID))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("revokeLink not-found status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// --- Unexpected errors ---

func TestHandler_UnexpectedError_Returns500(t *testing.T) {
	svc := &fakeService{createFn: func(_ string, _ CreateRequest) (*cairn.Link, error) {
		return nil, errors.New("boom")
	}}
	h := newHandlerWithFake(svc)

	w := do(t, h.createLink, http.MethodPost, "/links", `{"url":"https://example.com/a"}`)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("createLink unexpected error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// --- RegisterRoutes ---

func TestRegisterRoutes(t *testing.T) {
	svc := &fakeService{
		createFn: func(_ string, _ CreateRequest) (*cairn.Link, error) { return sampleLink(), nil },
		listFn: func(_ string, _ cairn.Cursor, _ int) ([]*cairn.Link, cairn.Cursor, error) {
			return []*cairn.Link{sampleLink()}, "", nil
		},
		revokeFn: func(_ string, _ cairn.Code) error { return nil },
	}
	h := newHandlerWithFake(svc)
	v1 := http.NewServeMux()
	h.RegisterRoutes(v1, passthroughAuth)

	cases := []struct {
		method string
		path   string
		body   string
		want   int
	}{
		{http.MethodPost, "/links", `{"url":"https://example.com/a"}`, http.StatusCreated},
		{http.MethodGet, "/links", "", http.StatusOK},
		{http.MethodDelete, "/links/abc123", "", http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			v1.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("RegisterRoutes %s %s status = %d, want %d", tc.method, tc.path, w.Code, tc.want)
			}
		})
	}
}
