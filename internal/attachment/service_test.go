package attachment

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// pngHeader / gifHeader / pdfHeader are the magic bytes
// http.DetectContentType keys on. They are spelled out here rather than
// generated so a test reads as "these bytes are a PNG", which is what the
// allow-list decision actually turns on.
var (
	pngHeader = []byte("\x89PNG\r\n\x1a\n")
	gifHeader = []byte("GIF89a")
	pdfHeader = []byte("%PDF-1.7\n")
	elfHeader = []byte("\x7fELF\x02\x01\x01\x00")
)

func newServiceUnderTest(t *testing.T) (*Service, BlobStore) {
	t.Helper()

	repo := NewMemoryRepository(fixedOwnership)
	store := newTestStore(t)
	return NewService(repo, store, 1024), store
}

func TestUpload_StoresBytesAndMetadata(t *testing.T) {
	svc, store := newServiceUnderTest(t)
	content := append(append([]byte{}, pngHeader...), []byte("body")...)

	att, err := svc.Upload(context.Background(), ownerID, ownedTaskID, "photo.png", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Upload() unexpected error: %v", err)
	}

	if att.ContentType != "image/png" {
		t.Errorf("ContentType = %q, want image/png", att.ContentType)
	}
	if att.SizeBytes != int64(len(content)) {
		t.Errorf("SizeBytes = %d, want %d", att.SizeBytes, len(content))
	}
	if att.OriginalFilename != "photo.png" {
		t.Errorf("OriginalFilename = %q, want photo.png", att.OriginalFilename)
	}
	if att.StorageKey == "" || att.StorageKey == att.ID {
		t.Errorf("StorageKey = %q — must be its own generated value, not the ID", att.StorageKey)
	}

	blob, err := store.Open(context.Background(), att.StorageKey)
	if err != nil {
		t.Fatalf("stored blob is not readable: %v", err)
	}
	defer blob.Close()
	got, _ := io.ReadAll(blob)
	if !bytes.Equal(got, content) {
		t.Errorf("stored bytes = %q, want %q", got, content)
	}
}

// TestUpload_ContentTypeComesFromBytesNotTheClient is the point of the
// allow-list. A client that labels an executable as a PNG must not get
// it stored: the header it wrote is a claim, and the bytes are the fact.
func TestUpload_ContentTypeComesFromBytesNotTheClient(t *testing.T) {
	svc, _ := newServiceUnderTest(t)

	_, err := svc.Upload(context.Background(), ownerID, ownedTaskID, "innocent.png", bytes.NewReader(elfHeader))
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Upload() of an ELF binary named .png: error = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "octet-stream") {
		t.Errorf("error = %q, want it to name the *detected* type so a caller can see what the server made of the bytes", err)
	}
}

func TestUpload_AcceptsEveryAllowedType(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    string
	}{
		{"png", pngHeader, "image/png"},
		{"gif", gifHeader, "image/gif"},
		{"pdf", pdfHeader, "application/pdf"},
		{"plain text", []byte("just some notes"), "text/plain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newServiceUnderTest(t)

			att, err := svc.Upload(context.Background(), ownerID, ownedTaskID, "file", bytes.NewReader(tt.content))
			if err != nil {
				t.Fatalf("Upload() unexpected error: %v", err)
			}
			if att.ContentType != tt.want {
				t.Errorf("ContentType = %q, want %q", att.ContentType, tt.want)
			}
		})
	}
}

// TestUpload_RejectsHTML pins the omission that is deliberate rather than
// accidental: HTML served from this origin would run as same-origin
// script if a browser ever rendered it.
func TestUpload_RejectsHTML(t *testing.T) {
	svc, _ := newServiceUnderTest(t)

	_, err := svc.Upload(context.Background(), ownerID, ownedTaskID, "page.html",
		strings.NewReader("<!DOCTYPE html><html><body><script>alert(1)</script></body></html>"))
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Upload() of HTML: error = %v, want ErrInvalidInput", err)
	}
}

func TestUpload_RejectsOversized(t *testing.T) {
	repo := NewMemoryRepository(fixedOwnership)
	svc := NewService(repo, newTestStore(t), 10)

	content := append(append([]byte{}, pngHeader...), bytes.Repeat([]byte("x"), 100)...)

	_, err := svc.Upload(context.Background(), ownerID, ownedTaskID, "big.png", bytes.NewReader(content))
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Upload() over the limit: error = %v, want ErrInvalidInput", err)
	}
}

// TestUpload_OnSomeoneElsesTask_LeavesNoOrphanBlob covers the ordering
// this Service deliberately chose. Bytes are written before metadata, so
// a rejected metadata write has already produced a file — and it has to
// be cleaned up, or every refused upload leaks disk.
func TestUpload_OnSomeoneElsesTask_LeavesNoOrphanBlob(t *testing.T) {
	repo := NewMemoryRepository(fixedOwnership)
	store := newTestStore(t)
	svc := NewService(repo, store, 1024)

	_, err := svc.Upload(context.Background(), ownerID, otherTaskID, "photo.png", bytes.NewReader(pngHeader))
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("Upload() to another user's task: error = %v, want ErrTaskNotFound", err)
	}

	counting, ok := store.(*fsBlobStore)
	if !ok {
		t.Fatal("test store is not an fsBlobStore")
	}
	entries, err := readDirNames(counting.guard.Root())
	if err != nil {
		t.Fatalf("read storage dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("storage directory holds %v after a refused upload, want nothing", entries)
	}
}

func TestUpload_NormalizesFilename(t *testing.T) {
	tests := []struct {
		name  string
		given string
		want  string
	}{
		{"unix path", "/home/me/report.pdf", "report.pdf"},
		{"windows path", `C:\Users\me\report.pdf`, "report.pdf"},
		{"traversal in the name", "../../etc/passwd", "passwd"},
		{"control characters", "rep\x00ort\x1b.pdf", "report.pdf"},
		{"collapsed whitespace", "my    report.pdf", "my report.pdf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newServiceUnderTest(t)

			att, err := svc.Upload(context.Background(), ownerID, ownedTaskID, tt.given, bytes.NewReader(pdfHeader))
			if err != nil {
				t.Fatalf("Upload() unexpected error: %v", err)
			}
			if att.OriginalFilename != tt.want {
				t.Errorf("OriginalFilename = %q, want %q", att.OriginalFilename, tt.want)
			}
		})
	}
}

func TestUpload_RejectsEmptyFilename(t *testing.T) {
	svc, _ := newServiceUnderTest(t)

	for _, name := range []string{"", "   ", "/", "..", "///"} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.Upload(context.Background(), ownerID, ownedTaskID, name, bytes.NewReader(pdfHeader)); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("Upload() with filename %q: error = %v, want ErrInvalidInput", name, err)
			}
		})
	}
}

func TestDownload_ReturnsMetadataAndBytes(t *testing.T) {
	svc, _ := newServiceUnderTest(t)
	content := append(append([]byte{}, pdfHeader...), []byte("body")...)

	uploaded, err := svc.Upload(context.Background(), ownerID, ownedTaskID, "report.pdf", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Upload() unexpected error: %v", err)
	}

	att, blob, err := svc.Download(context.Background(), ownerID, uploaded.StorageKey)
	if err != nil {
		t.Fatalf("Download() unexpected error: %v", err)
	}
	defer blob.Close()

	if att.ID != uploaded.ID {
		t.Errorf("Download() returned attachment %q, want %q", att.ID, uploaded.ID)
	}
	got, _ := io.ReadAll(blob)
	if !bytes.Equal(got, content) {
		t.Errorf("downloaded bytes = %q, want %q", got, content)
	}
}

// TestDownload_KeyFromAnotherUserIsNotFound is the authorization test
// that matters most here: the caller has a real, valid storage key and
// still must be refused, indistinguishably from a key that names nothing.
func TestDownload_KeyFromAnotherUserIsNotFound(t *testing.T) {
	svc, _ := newServiceUnderTest(t)

	uploaded, err := svc.Upload(context.Background(), ownerID, ownedTaskID, "report.pdf", bytes.NewReader(pdfHeader))
	if err != nil {
		t.Fatalf("Upload() unexpected error: %v", err)
	}

	if _, _, err := svc.Download(context.Background(), strangerID, uploaded.StorageKey); !errors.Is(err, ErrNotFound) {
		t.Errorf("Download() with another user's key: error = %v, want ErrNotFound", err)
	}
}

func TestListByTask_ReturnsUploads(t *testing.T) {
	svc, _ := newServiceUnderTest(t)

	if _, err := svc.Upload(context.Background(), ownerID, ownedTaskID, "a.pdf", bytes.NewReader(pdfHeader)); err != nil {
		t.Fatalf("Upload() unexpected error: %v", err)
	}

	got, err := svc.ListByTask(context.Background(), ownerID, ownedTaskID)
	if err != nil {
		t.Fatalf("ListByTask() unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("ListByTask() returned %d attachments, want 1", len(got))
	}

	if _, err := svc.ListByTask(context.Background(), strangerID, ownedTaskID); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("ListByTask() for a stranger: error = %v, want ErrTaskNotFound", err)
	}
}
