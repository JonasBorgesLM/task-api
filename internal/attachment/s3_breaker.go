package attachment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/JonasBorgesLM/bastion"
	"github.com/minio/minio-go/v7"
)

// s3BreakerOpenTimeout is how long the breakers built by
// NewBreakerBlobStore stay Open before admitting a probe call (FR-01),
// and the exact value ErrUnavailable's Retry-After is derived from. One
// constant rather than two numbers that happen to agree today: the two
// are the same fact — "how long until this is worth trying again" — and
// keeping them one declaration is what stops a future change to one from
// quietly leaving the other behind.
const s3BreakerOpenTimeout = 30 * time.Second

// s3BreakerFailureThreshold is deliberately lower than bastion's own
// default (5): a credential or endpoint problem (see
// docs/DECISIONS.md § "Validar permissão de escrita no S3 na subida")
// is caught at startup, so a failure reaching this breaker at all is
// already a live dependency problem, not a configuration one — there is
// little value in absorbing more than a handful before saying so.
const s3BreakerFailureThreshold = 3

// breakerBlobStore decorates a BlobStore backed by a network dependency
// with two independent bastion breakers — see docs/DECISIONS.md §
// "Adoção do bastion" for why S3 specifically, and why List gets its own
// breaker rather than sharing rw's.
//
// This is the "decorator" shape bastion's own README documents: it also
// implements BlobStore, wraps another BlobStore, and is the only place
// in this package that imports bastion. Nothing above it — Service,
// Handler, handleServiceError — ever sees a bastion type; every sentinel
// this file's methods can return is this package's own (see
// translateBreakerError).
type breakerBlobStore struct {
	next BlobStore
	rw   *bastion.Breaker // Put, Delete, Open — the request path
	list *bastion.Breaker // List only — the background orphan sweep
}

// NewBreakerBlobStore wraps next with two breakers, both built with the
// same failure classifier (isS3BreakerFailure) and the same hooks. hooks
// is the caller's (cmd/api's) seam to log transitions through its own
// *slog.Logger and to read Counts() for /debug/vars — see
// docs/DECISIONS.md § "Counts() no /debug/vars".
//
// Restricted to the S3 branch of buildBlobStore: fsBlobStore is local
// disk, has no network round trip and no cascading failure to prevent,
// so wrapping it would add a failure mode to a path that has none.
//
// Returns the two breakers alongside the store so the caller can publish
// their Counts() without a type assertion back into this package.
func NewBreakerBlobStore(next BlobStore, hooks bastion.Hooks) (BlobStore, *bastion.Breaker, *bastion.Breaker, error) {
	opts := []bastion.Option{
		bastion.WithIsFailure(isS3BreakerFailure),
		bastion.WithOpenTimeout(s3BreakerOpenTimeout),
		bastion.WithFailureThreshold(s3BreakerFailureThreshold),
		bastion.WithHooks(hooks),
	}

	rw, err := bastion.New("attachment-s3", opts...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("attachment: build breaker: %w", err)
	}
	list, err := bastion.New("attachment-s3-list", opts...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("attachment: build breaker: %w", err)
	}

	return &breakerBlobStore{next: next, rw: rw, list: list}, rw, list, nil
}

func (s *breakerBlobStore) Put(ctx context.Context, key string, r io.Reader, maxBytes int64) (int64, error) {
	n, err := bastion.Execute(ctx, s.rw, func(ctx context.Context) (int64, error) {
		return s.next.Put(ctx, key, r, maxBytes)
	})
	return n, translateBreakerError(err, s3BreakerOpenTimeout)
}

func (s *breakerBlobStore) Delete(ctx context.Context, key string) error {
	err := bastion.Do(ctx, s.rw, func(ctx context.Context) error {
		return s.next.Delete(ctx, key)
	})
	return translateBreakerError(err, s3BreakerOpenTimeout)
}

// Open covers only the round trip that proves the object exists and the
// call that opens the stream — StatObject and the lazy, I/O-free
// GetObject inside s3BlobStore.Open. Every Read the handler performs
// afterward, streaming the download through http.ServeContent, happens
// outside this call and so outside the breaker: an object store that
// accepts the open and then hangs mid-stream is invisible here. Accepted
// and documented in docs/DECISIONS.md § "Adoção do bastion", not a gap
// to close later — closing it would mean bounding the download itself,
// which is exactly the mistake s3BlobStore.Open's own doc comment
// already rejects.
func (s *breakerBlobStore) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	blob, err := bastion.Execute(ctx, s.rw, func(ctx context.Context) (io.ReadSeekCloser, error) {
		return s.next.Open(ctx, key)
	})
	return blob, translateBreakerError(err, s3BreakerOpenTimeout)
}

// List runs through its own breaker, separate from rw: it only ever runs
// from the orphan collector (Service.CollectOrphans), off the request
// path and explicitly unbounded in size (BlobStore.List's own doc
// comment). Sharing rw would let one slow background sweep open the
// circuit in front of every user's upload and download.
func (s *breakerBlobStore) List(ctx context.Context) ([]BlobRef, error) {
	refs, err := bastion.Execute(ctx, s.list, func(ctx context.Context) ([]BlobRef, error) {
		return s.next.List(ctx)
	})
	return refs, translateBreakerError(err, s3BreakerOpenTimeout)
}

// translateBreakerError maps bastion's own rejection sentinels to this
// package's ErrUnavailable at the boundary, so nothing above this file
// ever needs bastion in scope to handle the "breaker said no" case —
// the same rule CLAUDE.md states for handleServiceError and PostgreSQL,
// applied to a different dependency underneath the same interface.
// retryAfter is the calling breaker's own openTimeout — the S3
// breakers built in this file pass s3BreakerOpenTimeout; the shared
// PostgreSQL breaker this package's own Repository decorator uses
// (postgres_breaker.go) passes whatever cmd/api built it with, since
// that breaker is shared across three packages and none of them owns
// the number.
//
// Every other error — including one returned by s.next itself, already
// in this package's own vocabulary (ErrNotFound, ErrTooLarge, or a
// generic wrapped fmt.Errorf) — passes through unchanged: op's own
// errors were never bastion's to translate.
func translateBreakerError(err error, retryAfter time.Duration) error {
	if errors.Is(err, bastion.ErrOpenState) || errors.Is(err, bastion.ErrTooManyRequests) {
		return &unavailableError{cause: err, retryAfter: retryAfter}
	}
	return err
}

// unavailableError carries ErrUnavailable plus how long the caller
// should wait before retrying, without ever exposing the bastion
// sentinel it was built from through Unwrap/errors.As — a handler has no
// business inspecting bastion.ErrOpenState directly, only this
// package's own ErrUnavailable, matched through Is below.
//
// Shared by both breakers that can produce an ErrUnavailable for this
// package — the BlobStore breaker in this file and the shared
// PostgreSQL breaker's Repository decorator (postgres_breaker.go) — so
// a caller (Handler included) never needs to know which dependency
// underneath actually rejected the call, only that one did and for how
// long to back off. retryAfter carries that per rejection rather than
// being read from a package constant, since the two breakers this type
// represents do not share one.
type unavailableError struct {
	cause      error
	retryAfter time.Duration
}

func (e *unavailableError) Error() string {
	return fmt.Sprintf("%s: %s", ErrUnavailable, e.cause)
}

// Is makes errors.Is(err, ErrUnavailable) succeed. Deliberately not an
// Unwrap: unwrapping would also make errors.Is/As reach e.cause, which
// is a bastion sentinel this package's own callers must never be able to
// match against directly.
func (e *unavailableError) Is(target error) bool {
	return target == ErrUnavailable
}

// RetryAfter reports how long the caller should wait before retrying.
// Handler reads this via errors.As against the
// interface{ RetryAfter() time.Duration } shape, exactly the way the
// standard library's own net/url and net/http errors expose extra detail
// without a caller needing this file's concrete type.
func (e *unavailableError) RetryAfter() time.Duration {
	return e.retryAfter
}

// isS3BreakerFailure classifies which errors returned by the wrapped
// BlobStore count against the breaker (FR-04). Written the way bastion's
// own WithIsFailure doc comment asks — an allowlist of what is broken,
// not an exclusion list of what is fine — and for the same reason
// internal/platform/pgerr.IsInfrastructureFailure (issue #278/16.A2) is:
// Put, Open and List all share one return path between a genuinely
// broken object store and an answer about the caller's own request.
//
// ErrNotFound and ErrTooLarge are a healthy store's honest answers —
// counting either as a breaker failure would let a client enumerating
// keys, or simply uploading an oversized file in a loop, open the
// circuit on an S3 that is working perfectly. Everything else this
// function recognizes is a signal that the object store or the network
// to it, not the request, is the problem; anything it does not
// recognize returns false, the same "ignored until somebody adds it"
// default WithIsFailure's own doc comment calls out.
func isS3BreakerFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrTooLarge) {
		return false
	}

	// s3BlobStore.withTimeout derives its own context from the caller's;
	// a DeadlineExceeded surfacing here is that internally-derived
	// deadline elapsing, not the caller's own context (bastion's Execute
	// already excludes that case before classify is ever reached — see
	// [bastion.Execute]'s doc comment on ctx.Err()). The object store did
	// not answer in time, which is exactly what a breaker exists to act
	// on.
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// A transport-level failure — connection refused, DNS, TLS, a reset
	// mid-request — reaching all the way out of minio-go's own retry
	// loop (10 attempts, see bastion.Retry's doc comment) means the
	// object store was not reachable at all for the whole retry budget.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	// A well-formed S3 error response, decoded the same way
	// mapS3Error decodes it: a 5xx status, or one of the codes MinIO and
	// S3 both document as the server itself being overloaded or
	// mid-recovery rather than answering the request. A 4xx that is not
	// one of these (AccessDenied included) is a verdict about the
	// request or the credential, not the dependency's health — see
	// docs/DECISIONS.md § "Validar permissão de escrita no S3 na subida"
	// for why AccessDenied specifically is caught at startup instead.
	resp := minio.ToErrorResponse(err)
	switch {
	case resp.StatusCode >= 500:
		return true
	case resp.Code == "SlowDown", resp.Code == "ServiceUnavailable", resp.Code == "InternalError":
		return true
	}

	return false
}
