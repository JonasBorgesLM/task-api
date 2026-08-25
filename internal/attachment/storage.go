package attachment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/JonasBorgesLM/moat/pathguard"
)

// ErrTooLarge is returned by BlobStore.Put when the incoming stream
// exceeds the configured limit. It is separate from ErrInvalidInput
// because the caller learns it only part-way through writing, after the
// blob has already begun to land — a distinction that matters to whoever
// has to clean up.
var ErrTooLarge = errors.New("attachment exceeds the maximum size")

// BlobStore stores the bytes an Attachment points at. It is a separate
// boundary from Repository because the two fail independently and are
// backed by entirely different things: losing a row and losing a file are
// different incidents with different recoveries.
//
// Keys are the Attachment.StorageKey values Service generates. A store
// must treat them as opaque names, never as paths — the filesystem
// implementation below relies on that, and on the keys being UUIDs, but
// enforces containment regardless rather than trusting either.
type BlobStore interface {
	// Put writes r under key, stopping with ErrTooLarge if more than
	// maxBytes arrive. It returns the number of bytes actually written,
	// which is what Service records: a length the client declared is a
	// claim, and this is a measurement.
	//
	// A Put that returns an error leaves nothing behind under key.
	Put(ctx context.Context, key string, r io.Reader, maxBytes int64) (int64, error)

	// Open returns the stored bytes for reading. The caller closes it.
	Open(ctx context.Context, key string) (io.ReadSeekCloser, error)

	// Delete removes the blob. Deleting a key that is not there is not an
	// error: the caller's intent is that the bytes be gone, and they are.
	Delete(ctx context.Context, key string) error
}

// fsBlobStore stores blobs as files under one directory, through a
// pathguard.Guard.
//
// The Guard is what makes a user-influenced name safe to use. Every path
// component is checked by the kernel as it is traversed, so a symlink
// planted inside the root — before the request or between two of its
// syscalls — cannot redirect a read or a write outside. That is a
// stronger guarantee than validating the string first and then calling
// os.Open, which re-resolves every component and reopens the race.
//
// The keys this store is given are already UUIDs, so none of them can
// contain a separator or a dot segment in the first place. The Guard is
// not there because the keys are untrusted; it is there so that the
// safety of this code does not depend on that remaining true. A future
// change to how keys are generated should not be able to turn this into
// a traversal bug.
type fsBlobStore struct {
	guard *pathguard.Guard
}

// NewFSBlobStore returns a BlobStore rooted at dir, which must already
// exist.
//
// A missing directory is an error rather than something created here, and
// that is pathguard's rule rather than this package's preference: silently
// creating it is how a typo in configuration becomes an empty tree that
// serves nothing while nobody notices. The caller closes the returned
// store to release the Guard's directory handle.
func NewFSBlobStore(dir string) (BlobStore, func() error, error) {
	guard, err := pathguard.New(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("attachment: open storage root: %w", err)
	}
	return &fsBlobStore{guard: guard}, guard.Close, nil
}

func (s *fsBlobStore) Put(ctx context.Context, key string, r io.Reader, maxBytes int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	f, err := s.guard.Create(key)
	if err != nil {
		return 0, fmt.Errorf("attachment: create blob: %w", err)
	}

	// Every failure past this point removes the partial file. A blob
	// nobody can reach is not harmless — it occupies the key, so a retry
	// that happened to generate the same one would find it taken.
	cleanup := func() {
		f.Close()
		_ = s.remove(key)
	}

	// Copy one byte more than the limit allows: if that byte arrives, the
	// stream was over the limit, and we know it without having buffered
	// the whole thing to find out. The limit is enforced here rather than
	// from Content-Length because a client writes that header and can
	// simply understate it.
	written, err := io.Copy(f, io.LimitReader(r, maxBytes+1))
	if err != nil {
		cleanup()
		return 0, fmt.Errorf("attachment: write blob: %w", err)
	}
	if written > maxBytes {
		cleanup()
		return 0, ErrTooLarge
	}

	// Close before returning success, and report its error: a deferred
	// Close whose error is discarded can lose the tail of a buffered
	// write, leaving a blob that is silently short.
	if err := f.Close(); err != nil {
		_ = s.remove(key)
		return 0, fmt.Errorf("attachment: close blob: %w", err)
	}

	return written, nil
}

func (s *fsBlobStore) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f, err := s.guard.Open(key)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		// A containment failure lands here too, and is reported as
		// ErrNotFound for the same reason a stranger's attachment is:
		// the caller learns nothing about what does or does not exist
		// outside the root.
		return nil, ErrNotFound
	}
	return f, nil
}

func (s *fsBlobStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := s.remove(key); err != nil {
		return fmt.Errorf("attachment: delete blob: %w", err)
	}
	return nil
}

// remove deletes one blob.
//
// pathguard has no Remove of its own, so this is Resolve followed by
// os.Remove — and Resolve's documentation is explicit that the string it
// returns is not a TOCTOU-safe handle: every component is resolved again
// by the call that uses it. That warning is about reading or writing
// through an attacker-influenced path, and the residual exposure here is
// narrow but worth naming rather than glossing: the root is a directory
// this process owns, the key is a server-generated UUID that was never
// derived from client input, and Resolve has already refused anything
// that escapes the root. What remains is an attacker who can already
// create symlinks inside our storage directory, which is a compromise of
// the host rather than of this code path.
//
// A key that is not there is not an error. The caller's intent is that
// the bytes be gone, and they are.
func (s *fsBlobStore) remove(key string) error {
	path, err := s.guard.Resolve(key)
	if err != nil {
		// Covers both "no such blob" and "a name that escapes the root",
		// neither of which leaves anything to delete.
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
