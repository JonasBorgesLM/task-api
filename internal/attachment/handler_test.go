package attachment

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/JonasBorgesLM/task-api/internal/middleware"
)

// testUserID stands in for the authenticated caller — every route under
// test requires it in the request context, the same context key a real
// requireAuth middleware sets on success.
const testUserID = "user-1"

// passthroughAuth injects testUserID and never rejects, standing in for
// requireAuth in tests that only exercise routing/serialization — the
// same helper task and user's handler tests use.
func passthroughAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := middleware.ContextWithUserID(r.Context(), testUserID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// fakeService is a test double for attachmentService.
type fakeService struct {
	downloadFn func(userID, storageKey string) (Attachment, io.ReadSeekCloser, error)
	deleteFn   func(userID, storageKey string) error
}

func (f *fakeService) Upload(_ context.Context, _, _, _ string, _ io.Reader) (Attachment, error) {
	return Attachment{}, nil
}

func (f *fakeService) Download(_ context.Context, userID, storageKey string) (Attachment, io.ReadSeekCloser, error) {
	if f.downloadFn != nil {
		return f.downloadFn(userID, storageKey)
	}
	return Attachment{}, nil, ErrNotFound
}

func (f *fakeService) ListByTask(_ context.Context, _, _ string) ([]Attachment, error) {
	return nil, nil
}

func (f *fakeService) Delete(_ context.Context, userID, storageKey string) error {
	if f.deleteFn != nil {
		return f.deleteFn(userID, storageKey)
	}
	return nil
}

func newHandlerWithFake(svc *fakeService) *Handler {
	return NewHandler(svc, slog.New(slog.DiscardHandler), 1<<20)
}

// --- DELETE /files/{key} ---

func TestDelete_Handler_ReturnsNoContent(t *testing.T) {
	var calledWith [2]string
	svc := &fakeService{
		deleteFn: func(userID, storageKey string) error {
			calledWith = [2]string{userID, storageKey}
			return nil
		},
	}
	h := newHandlerWithFake(svc)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, passthroughAuth)

	req := httptest.NewRequest(http.MethodDelete, "/files/abc-123", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if calledWith != [2]string{testUserID, "abc-123"} {
		t.Errorf("Delete() called with %v, want [%q %q]", calledWith, testUserID, "abc-123")
	}
}

func TestDelete_Handler_NotFound(t *testing.T) {
	svc := &fakeService{
		deleteFn: func(_, _ string) error { return ErrNotFound },
	}
	h := newHandlerWithFake(svc)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, passthroughAuth)

	req := httptest.NewRequest(http.MethodDelete, "/files/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDelete_Handler_RequiresAuth(t *testing.T) {
	svc := &fakeService{}
	h := newHandlerWithFake(svc)
	mux := http.NewServeMux()
	// A middleware that always rejects, standing in for a real
	// requireAuth seeing no valid token.
	rejectAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
	}
	h.RegisterRoutes(mux, rejectAuth)

	req := httptest.NewRequest(http.MethodDelete, "/files/abc-123", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}
