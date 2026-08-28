package attachment

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
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

// --- orphan collection ---

// TestCollectOrphans_RemovesUnreferencedBlob covers the case the whole
// collector exists for: a task was deleted, its attachment rows cascaded
// away, and the bytes stayed behind.
func TestCollectOrphans_RemovesUnreferencedBlob(t *testing.T) {
	repo := NewMemoryRepository(fixedOwnership)
	store := newTestStore(t)
	svc := NewService(repo, store, 1024)

	// A blob with no row behind it — exactly what a cascaded delete
	// leaves on disk.
	if _, err := store.Put(context.Background(), "orphan-key", strings.NewReader("abandoned"), 1024); err != nil {
		t.Fatalf("Put() unexpected error: %v", err)
	}

	// Pretend it has been there a while. minAge is measured against the
	// Service's clock, which the test controls.
	svc.nowFunc = func() time.Time { return time.Now().Add(2 * time.Hour) }

	deleted, err := svc.CollectOrphans(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("CollectOrphans() unexpected error: %v", err)
	}
	if deleted != 1 {
		t.Errorf("CollectOrphans() removed %d, want 1", deleted)
	}
	if _, err := store.Open(context.Background(), "orphan-key"); !errors.Is(err, ErrNotFound) {
		t.Errorf("orphan blob still readable after collection: %v", err)
	}
}

// TestCollectOrphans_KeepsReferencedBlob is the assertion that keeps the
// collector from being a data-loss machine: a blob a row points at is
// old, unreferenced-looking to a naive sweep, and must survive.
func TestCollectOrphans_KeepsReferencedBlob(t *testing.T) {
	repo := NewMemoryRepository(fixedOwnership)
	store := newTestStore(t)
	svc := NewService(repo, store, 1024)

	att, err := svc.Upload(context.Background(), ownerID, ownedTaskID, "report.pdf", bytes.NewReader(pdfHeader))
	if err != nil {
		t.Fatalf("Upload() unexpected error: %v", err)
	}

	svc.nowFunc = func() time.Time { return time.Now().Add(48 * time.Hour) }

	deleted, err := svc.CollectOrphans(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("CollectOrphans() unexpected error: %v", err)
	}
	if deleted != 0 {
		t.Errorf("CollectOrphans() removed %d, want 0 — the blob is referenced", deleted)
	}
	if _, err := store.Open(context.Background(), att.StorageKey); err != nil {
		t.Errorf("referenced blob was deleted: %v", err)
	}
}

// TestCollectOrphans_SparesBlobsInsideTheGracePeriod is the test that
// matters most here, and the reason minAge exists at all.
//
// Upload writes the bytes before the metadata row. In that window a
// healthy upload is byte-for-byte indistinguishable from an orphan: a
// file no row references. A collector without a grace period would race
// every upload in flight and delete some of them — intermittently, under
// load, which is the worst way to discover it.
//
// The blob here is fresh and unreferenced, exactly as a mid-flight upload
// would be, and must be left alone.
func TestCollectOrphans_SparesBlobsInsideTheGracePeriod(t *testing.T) {
	repo := NewMemoryRepository(fixedOwnership)
	store := newTestStore(t)
	svc := NewService(repo, store, 1024)

	if _, err := store.Put(context.Background(), "upload-in-flight", strings.NewReader("bytes landed, row not yet written"), 1024); err != nil {
		t.Fatalf("Put() unexpected error: %v", err)
	}

	// The Service's clock is left alone, so the blob is brand new.
	deleted, err := svc.CollectOrphans(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("CollectOrphans() unexpected error: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("CollectOrphans() removed %d — it deleted a blob younger than the grace period, which is an upload in flight", deleted)
	}
	if _, err := store.Open(context.Background(), "upload-in-flight"); err != nil {
		t.Errorf("in-flight upload's blob was deleted: %v", err)
	}
}

// TestCollectOrphans_RejectsZeroMinAge closes the door on the shape of
// the bug above: a caller cannot ask for a collector with no grace
// period, however convenient that would be in a test.
func TestCollectOrphans_RejectsZeroMinAge(t *testing.T) {
	svc, _ := newServiceUnderTest(t)

	for _, minAge := range []time.Duration{0, -time.Hour} {
		if _, err := svc.CollectOrphans(context.Background(), minAge); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("CollectOrphans(%v) error = %v, want ErrInvalidInput", minAge, err)
		}
	}
}

// TestCollectOrphans_LeavesForeignFilesAlone covers the storage
// directory containing something this application did not write. The
// collector deletes files; anything it cannot positively identify as its
// own must survive.
func TestCollectOrphans_LeavesForeignFilesAlone(t *testing.T) {
	dir := t.TempDir()
	store, closeStore, err := NewFSBlobStore(dir)
	if err != nil {
		t.Fatalf("NewFSBlobStore() unexpected error: %v", err)
	}
	defer closeStore()

	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("not ours"), 0o600); err != nil {
		t.Fatalf("write foreign file: %v", err)
	}

	repo := &keyFilteringRepo{Repository: NewMemoryRepository(fixedOwnership)}
	svc := NewService(repo, store, 1024)
	svc.nowFunc = func() time.Time { return time.Now().Add(48 * time.Hour) }

	deleted, err := svc.CollectOrphans(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("CollectOrphans() unexpected error: %v", err)
	}
	if deleted != 0 {
		t.Errorf("CollectOrphans() removed %d, want 0 — a file that is not a storage key is not ours to delete", deleted)
	}
	if _, err := os.Stat(filepath.Join(dir, "README")); err != nil {
		t.Errorf("foreign file was deleted: %v", err)
	}
}

// keyFilteringRepo models what the PostgreSQL implementation does with a
// candidate that is not a UUID: it cannot be a storage key this
// application wrote, so it is never reported as unreferenced. The
// in-memory Repository has no such column type to filter on, so the
// behaviour is supplied here rather than being silently absent from the
// unit suite.
type keyFilteringRepo struct {
	Repository
}

func (r *keyFilteringRepo) UnreferencedKeys(ctx context.Context, keys []string) ([]string, error) {
	candidates := make([]string, 0, len(keys))
	for _, key := range keys {
		if uuidLike.MatchString(key) {
			candidates = append(candidates, key)
		}
	}
	return r.Repository.UnreferencedKeys(ctx, candidates)
}

var uuidLike = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// --- Delete ---

// failingDeleteStore wraps a real BlobStore so Delete always fails,
// letting a test observe Service.Delete's best-effort handling of that
// failure without needing a real storage backend to break on demand.
type failingDeleteStore struct {
	BlobStore
	deleteCalledWith string
}

func (s *failingDeleteStore) Delete(_ context.Context, key string) error {
	s.deleteCalledWith = key
	return errors.New("simulated blob store failure")
}

func TestDelete_RemovesMetadataAndBlob(t *testing.T) {
	store := newTestStore(t)
	repo := NewMemoryRepository(fixedOwnership)
	svc := NewService(repo, store, 1024)

	att, err := svc.Upload(context.Background(), ownerID, ownedTaskID, "photo.png", bytes.NewReader(pngHeader))
	if err != nil {
		t.Fatalf("Upload() unexpected error: %v", err)
	}

	if err := svc.Delete(context.Background(), ownerID, att.StorageKey); err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}

	if _, _, err := svc.Download(context.Background(), ownerID, att.StorageKey); !errors.Is(err, ErrNotFound) {
		t.Errorf("Download() after Delete() error = %v, want ErrNotFound", err)
	}
	if _, err := store.Open(context.Background(), att.StorageKey); !errors.Is(err, ErrNotFound) {
		t.Errorf("blob store Open() after Delete() error = %v, want ErrNotFound — the blob should be gone too", err)
	}
}

// TestDelete_OnSomeoneElsesAttachment_IsNotFound covers the ownership
// rule at the Service layer: a stranger's Delete must fail, and must
// touch neither the row nor the blob.
func TestDelete_OnSomeoneElsesAttachment_IsNotFound(t *testing.T) {
	store := newTestStore(t)
	repo := NewMemoryRepository(fixedOwnership)
	svc := NewService(repo, store, 1024)

	att, err := svc.Upload(context.Background(), ownerID, ownedTaskID, "photo.png", bytes.NewReader(pngHeader))
	if err != nil {
		t.Fatalf("Upload() unexpected error: %v", err)
	}

	if err := svc.Delete(context.Background(), strangerID, att.StorageKey); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() by a non-owner error = %v, want ErrNotFound", err)
	}

	// Still there, for the real owner.
	if _, _, err := svc.Download(context.Background(), ownerID, att.StorageKey); err != nil {
		t.Errorf("Download() after a refused Delete() unexpected error: %v", err)
	}
}

func TestDelete_UnknownKey_IsNotFound(t *testing.T) {
	svc, _ := newServiceUnderTest(t)

	if err := svc.Delete(context.Background(), ownerID, "does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete() error = %v, want ErrNotFound", err)
	}
}

// TestDelete_Twice_SecondCallIsNotFound is the Service-level shape of
// idempotency this package settled on: not a silent no-op (as
// user.Service.Logout is for an unknown token), but ErrNotFound on the
// second call — the same contract task.Service.DeleteTask already has.
// The two callers converge on the client-observable outcome that matters
// (the resource is gone either way), and DECISIONS.md records why a
// second call isn't instead treated as success.
func TestDelete_Twice_SecondCallIsNotFound(t *testing.T) {
	svc, _ := newServiceUnderTest(t)

	att, err := svc.Upload(context.Background(), ownerID, ownedTaskID, "photo.png", bytes.NewReader(pngHeader))
	if err != nil {
		t.Fatalf("Upload() unexpected error: %v", err)
	}
	if err := svc.Delete(context.Background(), ownerID, att.StorageKey); err != nil {
		t.Fatalf("first Delete() unexpected error: %v", err)
	}

	if err := svc.Delete(context.Background(), ownerID, att.StorageKey); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete() error = %v, want ErrNotFound", err)
	}
}

// TestDelete_BlobDeleteFails_StillSucceeds is the test that would have
// failed if the blob cleanup were not best-effort: with the row already
// gone (the client-observable half of "deleted"), a failure removing the
// underlying bytes must not turn a successful delete into an error
// response — see Service.Delete's doc comment and DECISIONS.md for why.
func TestDelete_BlobDeleteFails_StillSucceeds(t *testing.T) {
	repo := NewMemoryRepository(fixedOwnership)
	failing := &failingDeleteStore{BlobStore: newTestStore(t)}
	svc := NewService(repo, failing, 1024)

	att, err := svc.Upload(context.Background(), ownerID, ownedTaskID, "photo.png", bytes.NewReader(pngHeader))
	if err != nil {
		t.Fatalf("Upload() unexpected error: %v", err)
	}

	if err := svc.Delete(context.Background(), ownerID, att.StorageKey); err != nil {
		t.Errorf("Delete() with a failing blob store: unexpected error %v, want nil (best-effort blob cleanup)", err)
	}
	if failing.deleteCalledWith != att.StorageKey {
		t.Errorf("blob store Delete() called with %q, want %q — Delete() must still attempt the blob cleanup", failing.deleteCalledWith, att.StorageKey)
	}

	// The metadata row is gone regardless of the blob failure — that's
	// what "removed the metadata row first" means observably.
	if _, _, err := svc.Download(context.Background(), ownerID, att.StorageKey); !errors.Is(err, ErrNotFound) {
		t.Errorf("Download() after Delete() (blob cleanup failed) error = %v, want ErrNotFound", err)
	}
}
