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

// --- Malformed IDs (taskID and storageKey) ---
//
// These pin the fix for the same class of bug found in internal/task: a
// taskID or storageKey that isn't a syntactically valid UUID used to
// reach postgresRepository's ::uuid cast unchecked, surfacing as a 500
// instead of the routine 404/task-not-found a client typo deserves.
// isValidID rejects the shape before Repository is consulted at all.

// countingRepo wraps a real Repository and records whether each method
// was actually invoked, so a malformed-ID test can prove Service
// rejected the input itself rather than the wrapped Repository merely
// returning "not found" for an ID it didn't recognize — the two are
// observationally identical against memoryRepository (a malformed ID
// just isn't a key in its map either), which is exactly why call
// recording, not just the returned error, is what proves the check runs
// where it's supposed to.
type countingRepo struct {
	Repository
	createCalled           bool
	findByStorageKeyCalled bool
	findByTaskCalled       bool
}

func (r *countingRepo) Create(ctx context.Context, att Attachment, userID string) error {
	r.createCalled = true
	return r.Repository.Create(ctx, att, userID)
}

func (r *countingRepo) FindByStorageKey(ctx context.Context, storageKey, userID string) (Attachment, error) {
	r.findByStorageKeyCalled = true
	return r.Repository.FindByStorageKey(ctx, storageKey, userID)
}

func (r *countingRepo) FindByTask(ctx context.Context, taskID, userID string) ([]Attachment, error) {
	r.findByTaskCalled = true
	return r.Repository.FindByTask(ctx, taskID, userID)
}

// malformedIDs is the shared table every test below runs against —
// identical in spirit to task.malformedTaskIDs, duplicated rather than
// shared for the same reason the two packages don't import each other.
var malformedIDs = []struct {
	name string
	id   string
}{
	{"empty", ""},
	{"not_uuid_shaped_at_all", "not-a-uuid"},
	{"too_short", "11111111-1111-1111-1111-11111111"},
	{"too_long", "11111111-1111-1111-1111-1111111111111"},
	{"missing_hyphens", "11111111111111111111111111111111"},
	{"wrong_grouping", "111111111-111-1111-1111-111111111111"},
	{"non_hex_character", "1111111g-1111-1111-1111-111111111111"},
	{"sql_injection_attempt", "'; DROP TABLE tasks; --"},
}

func TestUpload_MalformedTaskID_IsTaskNotFound(t *testing.T) {
	for _, tc := range malformedIDs {
		t.Run(tc.name, func(t *testing.T) {
			repo := &countingRepo{Repository: NewMemoryRepository(fixedOwnership)}
			svc := NewService(repo, newTestStore(t), 1024)

			_, err := svc.Upload(context.Background(), ownerID, tc.id, "photo.png", bytes.NewReader(pngHeader))
			if !errors.Is(err, ErrTaskNotFound) {
				t.Errorf("Upload(taskID=%q) error = %v, want ErrTaskNotFound", tc.id, err)
			}
			if repo.createCalled {
				t.Errorf("Upload(taskID=%q) reached Repository.Create; isValidID should have rejected it first", tc.id)
			}
		})
	}
}

func TestDownload_MalformedStorageKey_IsNotFound(t *testing.T) {
	for _, tc := range malformedIDs {
		t.Run(tc.name, func(t *testing.T) {
			repo := &countingRepo{Repository: NewMemoryRepository(fixedOwnership)}
			svc := NewService(repo, newTestStore(t), 1024)

			_, _, err := svc.Download(context.Background(), ownerID, tc.id)
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("Download(storageKey=%q) error = %v, want ErrNotFound", tc.id, err)
			}
			if repo.findByStorageKeyCalled {
				t.Errorf("Download(storageKey=%q) reached Repository.FindByStorageKey; isValidID should have rejected it first", tc.id)
			}
		})
	}
}

func TestListByTask_MalformedTaskID_IsTaskNotFound(t *testing.T) {
	for _, tc := range malformedIDs {
		t.Run(tc.name, func(t *testing.T) {
			repo := &countingRepo{Repository: NewMemoryRepository(fixedOwnership)}
			svc := NewService(repo, newTestStore(t), 1024)

			_, err := svc.ListByTask(context.Background(), ownerID, tc.id)
			if !errors.Is(err, ErrTaskNotFound) {
				t.Errorf("ListByTask(taskID=%q) error = %v, want ErrTaskNotFound", tc.id, err)
			}
			if repo.findByTaskCalled {
				t.Errorf("ListByTask(taskID=%q) reached Repository.FindByTask; isValidID should have rejected it first", tc.id)
			}
		})
	}
}

// TestDownload_WellFormedKey_StillReachesRepository is the control case:
// a syntactically valid UUID that simply isn't stored must still reach
// Repository and get ErrNotFound from it, not from isValidID. Without
// this, the tests above could pass for the wrong reason — isValidID
// rejecting everything, valid IDs included.
func TestDownload_WellFormedKey_StillReachesRepository(t *testing.T) {
	repo := &countingRepo{Repository: NewMemoryRepository(fixedOwnership)}
	svc := NewService(repo, newTestStore(t), 1024)

	const wellFormedButUnknown = "99999999-9999-9999-9999-999999999999"
	_, _, err := svc.Download(context.Background(), ownerID, wellFormedButUnknown)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Download() error = %v, want ErrNotFound", err)
	}
	if !repo.findByStorageKeyCalled {
		t.Error("Download() with a well-formed key did not reach Repository.FindByStorageKey")
	}
}
