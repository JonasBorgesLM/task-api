package attachment

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) BlobStore {
	t.Helper()

	store, closeStore, err := NewFSBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFSBlobStore() unexpected error: %v", err)
	}
	t.Cleanup(func() {
		if err := closeStore(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

func TestFSBlobStore_PutAndOpen(t *testing.T) {
	store := newTestStore(t)
	const content = "the file contents"

	n, err := store.Put(context.Background(), "key-1", strings.NewReader(content), 1024)
	if err != nil {
		t.Fatalf("Put() unexpected error: %v", err)
	}
	if n != int64(len(content)) {
		t.Errorf("Put() wrote %d bytes, want %d", n, len(content))
	}

	blob, err := store.Open(context.Background(), "key-1")
	if err != nil {
		t.Fatalf("Open() unexpected error: %v", err)
	}
	defer blob.Close()

	got, err := io.ReadAll(blob)
	if err != nil {
		t.Fatalf("read blob: %v", err)
	}
	if string(got) != content {
		t.Errorf("blob = %q, want %q", got, content)
	}
}

// TestFSBlobStore_Put_OverLimit covers the boundary in both directions:
// exactly at the limit is stored, one byte past it is refused. The
// implementation reads one byte more than allowed to tell those apart,
// so an off-by-one here would silently accept oversized uploads.
func TestFSBlobStore_Put_OverLimit(t *testing.T) {
	store := newTestStore(t)

	if _, err := store.Put(context.Background(), "at-limit", bytes.NewReader(make([]byte, 100)), 100); err != nil {
		t.Errorf("Put() at exactly the limit: unexpected error: %v", err)
	}

	_, err := store.Put(context.Background(), "over-limit", bytes.NewReader(make([]byte, 101)), 100)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Put() one byte over the limit: error = %v, want ErrTooLarge", err)
	}
}

// TestFSBlobStore_Put_OverLimitLeavesNothing is the half that matters
// operationally: a rejected upload must not leave a partial file
// occupying its key, or a retry that regenerated the same key would find
// it taken.
func TestFSBlobStore_Put_OverLimitLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	store, closeStore, err := NewFSBlobStore(dir)
	if err != nil {
		t.Fatalf("NewFSBlobStore() unexpected error: %v", err)
	}
	defer closeStore()

	if _, err := store.Put(context.Background(), "rejected", bytes.NewReader(make([]byte, 500)), 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Put() error = %v, want ErrTooLarge", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read storage dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("storage directory contains %d entries after a rejected Put, want 0", len(entries))
	}
}

func TestFSBlobStore_Open_Missing(t *testing.T) {
	store := newTestStore(t)

	if _, err := store.Open(context.Background(), "never-written"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open() error = %v, want ErrNotFound", err)
	}
}

func TestFSBlobStore_Delete(t *testing.T) {
	store := newTestStore(t)

	if _, err := store.Put(context.Background(), "key-1", strings.NewReader("x"), 1024); err != nil {
		t.Fatalf("Put() unexpected error: %v", err)
	}
	if err := store.Delete(context.Background(), "key-1"); err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if _, err := store.Open(context.Background(), "key-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open() after Delete = %v, want ErrNotFound", err)
	}
}

// TestFSBlobStore_Delete_MissingIsNotAnError pins the contract: the
// caller's intent is that the bytes be gone. If they already are, that
// intent is satisfied.
func TestFSBlobStore_Delete_MissingIsNotAnError(t *testing.T) {
	store := newTestStore(t)

	if err := store.Delete(context.Background(), "never-written"); err != nil {
		t.Errorf("Delete() on a missing key = %v, want nil", err)
	}
}

// --- containment ---
//
// These are the tests #29 asks for. The keys this store is handed in
// production are server-generated UUIDs, so none of the inputs below can
// occur today — which is exactly why they are worth pinning. They assert
// that containment is a property of the store rather than a consequence
// of how keys currently happen to be generated, so a future change to
// that generation cannot quietly turn this into a traversal bug.

func TestFSBlobStore_Open_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()

	// A file that genuinely exists one level above the root, so a
	// successful traversal would return real content and the test would
	// fail loudly rather than passing because the target was missing.
	outside := filepath.Join(dir, "outside.txt")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	root := filepath.Join(dir, "blobs")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("create root: %v", err)
	}

	store, closeStore, err := NewFSBlobStore(root)
	if err != nil {
		t.Fatalf("NewFSBlobStore() unexpected error: %v", err)
	}
	defer closeStore()

	for _, key := range []string{
		"../outside.txt",
		"../../etc/passwd",
		"subdir/../../outside.txt",
		"./../outside.txt",
		outside, // an absolute path to a real file outside the root
	} {
		t.Run(key, func(t *testing.T) {
			blob, err := store.Open(context.Background(), key)
			if err == nil {
				content, _ := io.ReadAll(blob)
				blob.Close()
				t.Fatalf("Open(%q) succeeded and returned %q — traversal escaped the root", key, content)
			}
			if !errors.Is(err, ErrNotFound) {
				t.Errorf("Open(%q) error = %v, want ErrNotFound", key, err)
			}
		})
	}
}

// TestFSBlobStore_Open_RejectsSymlinkEscape covers the case a string
// check cannot catch: the key contains no traversal sequence at all, and
// the escape happens in the filesystem instead.
func TestFSBlobStore_Open_RejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()

	outside := filepath.Join(dir, "outside.txt")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	root := filepath.Join(dir, "blobs")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("create root: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "innocent-looking-key")); err != nil {
		t.Skipf("cannot create symlinks on this platform: %v", err)
	}

	store, closeStore, err := NewFSBlobStore(root)
	if err != nil {
		t.Fatalf("NewFSBlobStore() unexpected error: %v", err)
	}
	defer closeStore()

	blob, err := store.Open(context.Background(), "innocent-looking-key")
	if err == nil {
		content, _ := io.ReadAll(blob)
		blob.Close()
		t.Fatalf("Open() followed a symlink out of the root and returned %q", content)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Open() error = %v, want ErrNotFound", err)
	}
}

// TestFSBlobStore_Put_RejectsTraversal is the write-side counterpart.
// An escape here is worse than a read: it overwrites rather than
// discloses.
func TestFSBlobStore_Put_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()

	outside := filepath.Join(dir, "outside.txt")
	if err := os.WriteFile(outside, []byte("ORIGINAL"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	root := filepath.Join(dir, "blobs")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("create root: %v", err)
	}

	store, closeStore, err := NewFSBlobStore(root)
	if err != nil {
		t.Fatalf("NewFSBlobStore() unexpected error: %v", err)
	}
	defer closeStore()

	if _, err := store.Put(context.Background(), "../outside.txt", strings.NewReader("OVERWRITTEN"), 1024); err == nil {
		t.Error("Put() with a traversing key succeeded, want an error")
	}

	content, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read outside file: %v", err)
	}
	if string(content) != "ORIGINAL" {
		t.Errorf("file outside the root was modified: %q", content)
	}
}

// TestNewFSBlobStore_MissingDirectory pins pathguard's rule: a missing
// root is an error, not something created on demand. Creating it would
// turn a typo in configuration into an empty tree that serves nothing
// while nobody notices.
func TestNewFSBlobStore_MissingDirectory(t *testing.T) {
	_, _, err := NewFSBlobStore(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Error("NewFSBlobStore() on a missing directory = nil, want an error")
	}
}

// readDirNames lists a directory's entries by name, for tests asserting
// that a failed operation left nothing behind.
func readDirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

// FuzzFSBlobStore_OpenNeverEscapesRoot is the fuzz target #29 asks for.
//
// The issue proposed running moat's own FuzzResolve. This fuzzes our use
// of it instead, which is the part that can actually regress here: the
// library's containment is the library's to test, while whether this
// store still routes every key through it is ours. A refactor that
// replaced guard.Open with os.Open would leave moat's fuzz target green
// and this one failing on the first traversal input.
//
// The invariant is deliberately narrow, so that it cannot become vacuous:
// for *any* key, Open either fails or returns bytes from inside the root.
// A file with known content sits just outside, so an escape produces a
// concrete, recognisable failure rather than a subtle one.
func FuzzFSBlobStore_OpenNeverEscapesRoot(f *testing.F) {
	dir := f.TempDir()

	const secret = "THIS FILE IS OUTSIDE THE ROOT"
	if err := os.WriteFile(filepath.Join(dir, "outside.txt"), []byte(secret), 0o600); err != nil {
		f.Fatalf("write outside file: %v", err)
	}

	root := filepath.Join(dir, "blobs")
	if err := os.Mkdir(root, 0o700); err != nil {
		f.Fatalf("create root: %v", err)
	}

	store, closeStore, err := NewFSBlobStore(root)
	if err != nil {
		f.Fatalf("NewFSBlobStore() unexpected error: %v", err)
	}
	f.Cleanup(func() { closeStore() })

	// One legitimate blob, so the target exercises the success path too
	// and a change that broke Open outright would not look like a pass.
	if _, err := store.Put(context.Background(), "real-key", strings.NewReader("inside"), 1024); err != nil {
		f.Fatalf("Put() unexpected error: %v", err)
	}

	for _, seed := range []string{
		"real-key",
		"../outside.txt",
		"..%2Foutside.txt",
		"....//outside.txt",
		"subdir/../../outside.txt",
		"/etc/passwd",
		filepath.Join(dir, "outside.txt"),
		"",
		".",
		"..",
		"\x00../outside.txt",
		strings.Repeat("../", 64) + "outside.txt",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, key string) {
		blob, err := store.Open(context.Background(), key)
		if err != nil {
			// Refusing is always an acceptable answer.
			return
		}
		defer blob.Close()

		content, err := io.ReadAll(blob)
		if err != nil {
			return
		}
		if string(content) == secret {
			t.Fatalf("Open(%q) escaped the root and read a file outside it", key)
		}

		// Beyond the sentinel, confirm containment structurally: a
		// successful open must have landed on a path under the root.
		file, ok := blob.(*os.File)
		if !ok {
			return
		}
		resolved, err := filepath.EvalSymlinks(file.Name())
		if err != nil {
			return
		}
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatalf("resolve root: %v", err)
		}
		if rel, err := filepath.Rel(realRoot, resolved); err != nil || strings.HasPrefix(rel, "..") {
			t.Fatalf("Open(%q) returned a file at %q, which is outside the root %q", key, resolved, realRoot)
		}
	})
}
