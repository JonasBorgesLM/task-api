package attachment

// The BlobStore contract, as one set of assertions run against every
// implementation.
//
// This file carries no build tag on purpose, so both the unit suite (for
// fsBlobStore) and the integration suite (for s3BlobStore) compile it.
// The point is that neither implementation gets its own idea of what the
// interface means: an fs-only assertion is an assertion the S3 store can
// quietly fail in production, where nobody is looking.
//
// Only behaviour the interface actually promises belongs here. Anything
// specific to one backend — traversal containment, which is a property of
// the filesystem and of pathguard — stays in that backend's own file.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// runBlobStoreContract exercises everything BlobStore promises. newStore
// must return a store that is empty.
func runBlobStoreContract(t *testing.T, newStore func(t *testing.T) BlobStore) {
	t.Helper()

	t.Run("put then open round-trips the bytes", func(t *testing.T) {
		store := newStore(t)
		const content = "the file contents"

		n, err := store.Put(context.Background(), "key-1", strings.NewReader(content), 1024)
		if err != nil {
			t.Fatalf("Put() unexpected error: %v", err)
		}
		if n != int64(len(content)) {
			t.Errorf("Put() reported %d bytes, want %d", n, len(content))
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
	})

	t.Run("open reports a missing key as ErrNotFound", func(t *testing.T) {
		store := newStore(t)

		if _, err := store.Open(context.Background(), "never-written"); !errors.Is(err, ErrNotFound) {
			t.Errorf("Open() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("exactly at the limit is accepted", func(t *testing.T) {
		store := newStore(t)

		if _, err := store.Put(context.Background(), "at-limit", bytes.NewReader(make([]byte, 100)), 100); err != nil {
			t.Errorf("Put() at exactly the limit: unexpected error: %v", err)
		}
	})

	t.Run("one byte over the limit is ErrTooLarge and stores nothing", func(t *testing.T) {
		store := newStore(t)

		if _, err := store.Put(context.Background(), "over-limit", bytes.NewReader(make([]byte, 101)), 100); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("Put() one byte over: error = %v, want ErrTooLarge", err)
		}

		// A rejected upload must not leave its key occupied — a retry
		// that regenerated the same key would find it taken.
		if _, err := store.Open(context.Background(), "over-limit"); !errors.Is(err, ErrNotFound) {
			t.Errorf("Open() after a rejected Put = %v, want ErrNotFound", err)
		}
	})

	t.Run("delete removes the blob", func(t *testing.T) {
		store := newStore(t)

		if _, err := store.Put(context.Background(), "key-1", strings.NewReader("x"), 1024); err != nil {
			t.Fatalf("Put() unexpected error: %v", err)
		}
		if err := store.Delete(context.Background(), "key-1"); err != nil {
			t.Fatalf("Delete() unexpected error: %v", err)
		}
		if _, err := store.Open(context.Background(), "key-1"); !errors.Is(err, ErrNotFound) {
			t.Errorf("Open() after Delete = %v, want ErrNotFound", err)
		}
	})

	t.Run("deleting a missing key is not an error", func(t *testing.T) {
		store := newStore(t)

		if err := store.Delete(context.Background(), "never-written"); err != nil {
			t.Errorf("Delete() on a missing key = %v, want nil", err)
		}
	})

	t.Run("list reports keys with a usable modification time", func(t *testing.T) {
		store := newStore(t)
		before := time.Now().Add(-time.Minute)

		for _, key := range []string{"a", "b"} {
			if _, err := store.Put(context.Background(), key, strings.NewReader("x"), 1024); err != nil {
				t.Fatalf("Put(%s) unexpected error: %v", key, err)
			}
		}

		refs, err := store.List(context.Background())
		if err != nil {
			t.Fatalf("List() unexpected error: %v", err)
		}
		if len(refs) != 2 {
			t.Fatalf("List() returned %d refs, want 2", len(refs))
		}

		seen := map[string]bool{}
		for _, ref := range refs {
			seen[ref.Key] = true
			// The orphan collector's grace period is measured against
			// this. A zero or stale ModTime would make a fresh blob look
			// old enough to delete — which is the in-flight upload the
			// collector must never touch.
			if ref.ModTime.Before(before) {
				t.Errorf("List() key %q has ModTime %v, older than the blob itself", ref.Key, ref.ModTime)
			}
		}
		for _, key := range []string{"a", "b"} {
			if !seen[key] {
				t.Errorf("List() did not report key %q", key)
			}
		}
	})

	t.Run("list on an empty store returns nothing rather than failing", func(t *testing.T) {
		store := newStore(t)

		refs, err := store.List(context.Background())
		if err != nil {
			t.Fatalf("List() unexpected error: %v", err)
		}
		if len(refs) != 0 {
			t.Errorf("List() = %v, want empty", refs)
		}
	})
}
