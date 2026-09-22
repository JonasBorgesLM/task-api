package attachment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JonasBorgesLM/bastion"
	"github.com/minio/minio-go/v7"
)

// newBreakerTestStore wraps a fresh filesystem store with the same
// decorator production uses. The filesystem backend stands in for S3
// here on purpose: this test exercises the decorator's own pass-through
// behaviour, not the S3 client, and staying off a real network
// dependency is what lets it run in the plain (non-integration) suite.
// TestS3BlobStore_Contract in s3_storage_test.go is what proves the S3
// backend itself satisfies BlobStore; this proves wrapping it in a
// breaker still does.
func newBreakerTestStore(t *testing.T) BlobStore {
	t.Helper()

	next, closeStore, err := NewFSBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFSBlobStore() unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = closeStore() })

	wrapped, _, _, err := NewBreakerBlobStore(next, bastion.Hooks{})
	if err != nil {
		t.Fatalf("NewBreakerBlobStore() unexpected error: %v", err)
	}
	return wrapped
}

// TestBreakerBlobStore_Contract is 16.B2's first acceptance criterion:
// the decorator must not break the contract every other BlobStore
// implementation already satisfies.
func TestBreakerBlobStore_Contract(t *testing.T) {
	runBlobStoreContract(t, newBreakerTestStore)
}

// closingReader adapts a *bytes.Reader to io.ReadSeekCloser, the shape
// BlobStore.Open promises. Close is a no-op.
type closingReader struct{ *bytes.Reader }

func (closingReader) Close() error { return nil }

// spyBlobStore is a hand-written BlobStore double that counts calls and
// can be told to fail Put and/or List with an error isS3BreakerFailure
// classifies as a failure (context.DeadlineExceeded). Delete always
// succeeds and Open always succeeds, since neither is under test where
// this type is used.
type spyBlobStore struct {
	mu        sync.Mutex
	putCalls  int
	listCalls int
	openCalls int
	failPut   bool
	failList  bool
}

func (s *spyBlobStore) Put(_ context.Context, _ string, _ io.Reader, _ int64) (int64, error) {
	s.mu.Lock()
	s.putCalls++
	s.mu.Unlock()
	if s.failPut {
		return 0, context.DeadlineExceeded
	}
	return 0, nil
}

func (s *spyBlobStore) Open(_ context.Context, _ string) (io.ReadSeekCloser, error) {
	s.mu.Lock()
	s.openCalls++
	s.mu.Unlock()
	return closingReader{bytes.NewReader([]byte("x"))}, nil
}

func (s *spyBlobStore) Delete(_ context.Context, _ string) error { return nil }

func (s *spyBlobStore) List(_ context.Context) ([]BlobRef, error) {
	s.mu.Lock()
	s.listCalls++
	s.mu.Unlock()
	if s.failList {
		return nil, context.DeadlineExceeded
	}
	return nil, nil
}

func (s *spyBlobStore) counts() (put, list, open int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putCalls, s.listCalls, s.openCalls
}

// TestBreakerBlobStore_Put_DoesNotMultiplyAttempts is 16.B2's hard rule
// under direct test: Execute/Do never retries on their own (unlike
// bastion.Retry), so one failing Put must reach the wrapped store
// exactly once. A negative assertion ("Put returned an error") is
// satisfied by a call that never ran at all, so this counts the
// invocation rather than only checking the error.
func TestBreakerBlobStore_Put_DoesNotMultiplyAttempts(t *testing.T) {
	spy := &spyBlobStore{failPut: true}
	wrapped, _, _, err := NewBreakerBlobStore(spy, bastion.Hooks{})
	if err != nil {
		t.Fatalf("NewBreakerBlobStore() unexpected error: %v", err)
	}

	if _, err := wrapped.Put(context.Background(), "k", strings.NewReader("x"), 10); err == nil {
		t.Fatal("Put() against a failing store = nil error, want one")
	}

	if put, _, _ := spy.counts(); put != 1 {
		t.Errorf("Put() reached the wrapped store %d times, want exactly 1 — bastion.Do must never retry on its own", put)
	}
}

// TestBreakerBlobStore_ListAndRW_UseSeparateBreakers is 16.B2's other
// acceptance criterion: List has its own breaker, so tripping it must
// leave Open (on the rw breaker) untouched.
func TestBreakerBlobStore_ListAndRW_UseSeparateBreakers(t *testing.T) {
	spy := &spyBlobStore{failList: true}
	wrapped, rw, list, err := NewBreakerBlobStore(spy, bastion.Hooks{})
	if err != nil {
		t.Fatalf("NewBreakerBlobStore() unexpected error: %v", err)
	}

	for i := 0; i < s3BreakerFailureThreshold; i++ {
		if _, err := wrapped.List(context.Background()); err == nil {
			t.Fatalf("List() call %d against a failing store = nil error, want one", i)
		}
	}
	if got := list.State(); got != bastion.StateOpen {
		t.Fatalf("list breaker State() = %v after %d consecutive failures, want StateOpen", got, s3BreakerFailureThreshold)
	}
	if _, err := wrapped.List(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("List() with the list breaker open: error = %v, want ErrUnavailable", err)
	}

	if got := rw.State(); got != bastion.StateClosed {
		t.Fatalf("rw breaker State() = %v, want StateClosed — a failing List must never open the request-path breaker", got)
	}
	if _, err := wrapped.Open(context.Background(), "k"); err != nil {
		t.Errorf("Open() while only the list breaker is open: unexpected error: %v", err)
	}
}

// TestBreakerBlobStore_NotFoundAndTooLarge_NeverTripTheBreaker is the
// classifier's central claim under direct test, against real repeated
// calls rather than only the unit table below: a store that legitimately
// keeps answering "no such key" (or refusing an oversized upload) must
// never look, to the breaker, like a store that is failing.
func TestBreakerBlobStore_NotFoundAndTooLarge_NeverTripTheBreaker(t *testing.T) {
	wrapped := newBreakerTestStore(t)

	for i := 0; i < s3BreakerFailureThreshold*3; i++ {
		if _, err := wrapped.Open(context.Background(), "never-written"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Open() call %d: error = %v, want ErrNotFound", i, err)
		}
	}
	for i := 0; i < s3BreakerFailureThreshold*3; i++ {
		if _, err := wrapped.Put(context.Background(), "over-limit", bytes.NewReader(make([]byte, 11)), 10); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("Put() call %d: error = %v, want ErrTooLarge", i, err)
		}
	}

	// Open() again: if either loop above had counted against the
	// breaker, s3BreakerFailureThreshold would already have been
	// crossed many times over and this would come back ErrUnavailable
	// instead of ErrNotFound.
	if _, err := wrapped.Open(context.Background(), "still-never-written"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open() after repeated ErrNotFound/ErrTooLarge: error = %v, want ErrNotFound (breaker must still be Closed)", err)
	}
}

// TestBreakerBlobStore_CircuitOpen_ReturnsErrUnavailableWithRetryAfter
// pins 16.B3's whole point: the sentinel a caller (eventually Handler)
// sees for a rejected call, and the Retry-After value it carries.
func TestBreakerBlobStore_CircuitOpen_ReturnsErrUnavailableWithRetryAfter(t *testing.T) {
	spy := &spyBlobStore{failPut: true}
	wrapped, rw, _, err := NewBreakerBlobStore(spy, bastion.Hooks{})
	if err != nil {
		t.Fatalf("NewBreakerBlobStore() unexpected error: %v", err)
	}

	for i := 0; i < s3BreakerFailureThreshold; i++ {
		_, _ = wrapped.Put(context.Background(), "k", strings.NewReader("x"), 10)
	}
	if got := rw.State(); got != bastion.StateOpen {
		t.Fatalf("rw breaker State() = %v after %d consecutive failures, want StateOpen", got, s3BreakerFailureThreshold)
	}

	_, err = wrapped.Put(context.Background(), "k2", strings.NewReader("x"), 10)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Put() with the breaker open: error = %v, want ErrUnavailable", err)
	}

	var withRetryAfter interface{ RetryAfter() time.Duration }
	if !errors.As(err, &withRetryAfter) {
		t.Fatalf("errors.As() found no RetryAfter() on %v", err)
	}
	if got := withRetryAfter.RetryAfter(); got != s3BreakerOpenTimeout {
		t.Errorf("RetryAfter() = %v, want %v (s3BreakerOpenTimeout)", got, s3BreakerOpenTimeout)
	}
}

// TestIsS3BreakerFailure is the classifier's unit-level table, the same
// shape as internal/platform/pgerr's own — real error values, not
// invented ones, with the two known-safe sentinels and an entirely
// unrecognized error both pinned to false (the allowlist's whole point:
// an error this function has never seen must not trip the circuit).
func TestIsS3BreakerFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"not found", ErrNotFound, false},
		{"too large", ErrTooLarge, false},
		{"wrapped not found", fmt.Errorf("attachment: get object: %w", ErrNotFound), false},
		{"wrapped too large", fmt.Errorf("attachment: %w", ErrTooLarge), false},

		{"deadline exceeded", context.DeadlineExceeded, true},
		{"wrapped deadline exceeded", fmt.Errorf("attachment: put object: %w", context.DeadlineExceeded), true},
		{"network error", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, true},
		{"wrapped network error", fmt.Errorf("attachment: put object: %w", &net.OpError{Op: "dial", Err: errors.New("connection refused")}), true},
		{"minio internal error", minio.ErrorResponse{StatusCode: 500, Code: "InternalError"}, true},
		{"minio slow down", minio.ErrorResponse{StatusCode: 503, Code: "SlowDown"}, true},
		{"minio service unavailable", minio.ErrorResponse{StatusCode: 503, Code: "ServiceUnavailable"}, true},

		// AccessDenied is a verdict about the credential, not the
		// dependency's health — see docs/DECISIONS.md § "Validar
		// permissão de escrita no S3 na subida" for why that is caught
		// at startup instead of here.
		{"minio access denied", minio.ErrorResponse{StatusCode: 403, Code: "AccessDenied"}, false},
		{"minio no such key", minio.ErrorResponse{StatusCode: 404, Code: "NoSuchKey"}, false},

		// An error isS3BreakerFailure has never seen the shape of must
		// default to false — the allowlist's asymmetry (bastion's own
		// WithIsFailure doc comment): a missed failure under-trips,
		// which is safe, rather than over-tripping on a healthy
		// dependency.
		{"unrecognized error", errors.New("boom"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isS3BreakerFailure(tt.err); got != tt.want {
				t.Errorf("isS3BreakerFailure(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestUnavailableError_NeverMatchesTheUnderlyingBastionSentinel pins
// s3_breaker.go's own claim: unavailableError has no Unwrap, so a caller
// cannot reach bastion.ErrOpenState/ErrTooManyRequests through it via
// errors.Is/As — only ErrUnavailable itself.
func TestUnavailableError_NeverMatchesTheUnderlyingBastionSentinel(t *testing.T) {
	err := translateBreakerError(bastion.ErrOpenState, s3BreakerOpenTimeout)

	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("errors.Is(err, ErrUnavailable) = false, want true")
	}
	if errors.Is(err, bastion.ErrOpenState) {
		t.Errorf("errors.Is(err, bastion.ErrOpenState) = true, want false — the bastion sentinel must not leak through")
	}
}
