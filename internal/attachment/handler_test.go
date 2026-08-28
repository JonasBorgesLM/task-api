package attachment

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func newHandlerWithFake(svc *fakeService) *Handler {
	return NewHandler(svc, slog.New(slog.DiscardHandler), 1<<20)
}

// closingReadSeeker adapts a *bytes.Reader to io.ReadSeekCloser, the
// shape BlobStore.Open (and therefore Service.Download) actually
// returns. Close is a no-op — nothing in these tests needs to observe
// it.
type closingReadSeeker struct{ *bytes.Reader }

func (closingReadSeeker) Close() error { return nil }

// TestDownload_FullRequest_SetsContentLengthFromServeContent pins the
// case http.ServeContent handles on its own: no Range header, the whole
// object goes out, and Content-Length equals the full size.
//
// This is the behavior a handler-side `w.Header().Set("Content-Length",
// ...)` before calling ServeContent would have gotten right by
// coincidence — which is exactly why the wrong case (below) matters more
// than this one.
func TestDownload_FullRequest_SetsContentLengthFromServeContent(t *testing.T) {
	content := []byte("the quick brown fox jumps over the lazy dog")
	att := Attachment{
		StorageKey:       "11111111-1111-1111-1111-111111111111",
		OriginalFilename: "fox.txt",
		ContentType:      "text/plain",
		SizeBytes:        int64(len(content)),
		CreatedAt:        time.Now(),
	}
	svc := &fakeService{
		downloadFn: func(_, _ string) (Attachment, io.ReadSeekCloser, error) {
			return att, closingReadSeeker{bytes.NewReader(content)}, nil
		},
	}
	h := newHandlerWithFake(svc)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, passthroughAuth)

	req := httptest.NewRequest(http.MethodGet, "/files/"+att.StorageKey, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Header().Get("Content-Length"); got != "43" {
		t.Errorf("Content-Length = %q, want %q", got, "43")
	}
	if w.Body.Len() != len(content) {
		t.Errorf("body length = %d, want %d", w.Body.Len(), len(content))
	}
}

// TestDownload_RangeRequest_ContentLengthIsTheSliceNotTheObject documents
// why the handler no longer sets Content-Length before calling
// http.ServeContent.
//
// The removed line set it to att.SizeBytes (the full object), which
// ServeContent then always overwrote with the slice's own, smaller size
// on a Range request — verified by temporarily restoring that line and
// rerunning this exact test, which still passed. So the line was never a
// live bug: it was dead code stating an intent ("this response is
// SizeBytes long") that was false for any Range request and silently
// discarded by whatever ran after it. This test is what would catch a
// regression if a future change stopped delegating Content-Length to
// ServeContent — e.g. switching to a handler that does not overwrite it.
func TestDownload_RangeRequest_ContentLengthIsTheSliceNotTheObject(t *testing.T) {
	content := []byte("the quick brown fox jumps over the lazy dog") // 43 bytes
	att := Attachment{
		StorageKey:       "22222222-2222-2222-2222-222222222222",
		OriginalFilename: "fox.txt",
		ContentType:      "text/plain",
		SizeBytes:        int64(len(content)),
		CreatedAt:        time.Now(),
	}
	svc := &fakeService{
		downloadFn: func(_, _ string) (Attachment, io.ReadSeekCloser, error) {
			return att, closingReadSeeker{bytes.NewReader(content)}, nil
		},
	}
	h := newHandlerWithFake(svc)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, passthroughAuth)

	req := httptest.NewRequest(http.MethodGet, "/files/"+att.StorageKey, nil)
	req.Header.Set("Range", "bytes=4-8") // "quick" — 5 bytes, not the full 43
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusPartialContent)
	}
	if got := w.Header().Get("Content-Length"); got != "5" {
		t.Errorf("Content-Length = %q, want %q (the slice size, not SizeBytes=%d)",
			got, "5", att.SizeBytes)
	}
	if got, want := w.Header().Get("Content-Range"), "bytes 4-8/43"; got != want {
		t.Errorf("Content-Range = %q, want %q", got, want)
	}
	if got, want := w.Body.String(), "quick"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// TestDownload_AlwaysSetsContentDispositionAttachment pins the other
// header this handler is responsible for, unrelated to ServeContent:
// every download must offer to save rather than render, full request or
// Range alike — see Handler.download's doc comment for why.
func TestDownload_AlwaysSetsContentDispositionAttachment(t *testing.T) {
	content := []byte("data")
	att := Attachment{
		StorageKey:       "33333333-3333-3333-3333-333333333333",
		OriginalFilename: "notes.txt",
		ContentType:      "text/plain",
		SizeBytes:        int64(len(content)),
		CreatedAt:        time.Now(),
	}
	svc := &fakeService{
		downloadFn: func(_, _ string) (Attachment, io.ReadSeekCloser, error) {
			return att, closingReadSeeker{bytes.NewReader(content)}, nil
		},
	}
	h := newHandlerWithFake(svc)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux, passthroughAuth)

	req := httptest.NewRequest(http.MethodGet, "/files/"+att.StorageKey, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	want := `attachment; filename=notes.txt`
	if got := w.Header().Get("Content-Disposition"); got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
}
