package attachment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JonasBorgesLM/bastion"
)

// spyRepository is a minimal Repository double for this file's own
// tests — call-counted, no other behavior. attachment's Repository
// has no multi-statement transaction among its methods (unlike task's
// Update or user's CreateSession, see docs/DECISIONS.md § "Breaker
// compartilhado no PostgreSQL"), so Delete stands in as this package's
// representative write method for the "never reaches next while
// tripped" proof.
type spyRepository struct {
	deleteCalls int
	deleteErr   error
}

func (s *spyRepository) Create(context.Context, Attachment, string) error { return nil }
func (s *spyRepository) FindByStorageKey(context.Context, string, string) (Attachment, error) {
	return Attachment{}, nil
}
func (s *spyRepository) FindByTask(context.Context, string, string) ([]Attachment, error) {
	return nil, nil
}
func (s *spyRepository) Delete(context.Context, string, string) error {
	s.deleteCalls++
	return s.deleteErr
}
func (s *spyRepository) TotalBytesForUser(context.Context, string) (int64, error) { return 0, nil }
func (s *spyRepository) CountByTask(context.Context, string, string) (int, error) { return 0, nil }
func (s *spyRepository) UnreferencedKeys(context.Context, []string) ([]string, error) {
	return nil, nil
}

func newTestBreaker(t *testing.T) *bastion.Breaker {
	t.Helper()
	b, err := bastion.New("test", bastion.WithFailureThreshold(2))
	if err != nil {
		t.Fatalf("bastion.New() unexpected error: %v", err)
	}
	return b
}

const dbTestRetryAfter = 17 * time.Second

// TestPostgresBreakerRepository_TrippedCircuit_NeverReachesNext is
// 16.C2's central acceptance criterion, for this package's own
// decorator — see internal/task and internal/user's identically-named
// tests for the transactional cases this criterion was actually
// written for.
func TestPostgresBreakerRepository_TrippedCircuit_NeverReachesNext(t *testing.T) {
	spy := &spyRepository{}
	b := newTestBreaker(t)
	b.Trip(context.Background())

	repo := NewBreakerRepository(spy, b, dbTestRetryAfter)

	err := repo.Delete(context.Background(), "key", "user-1")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Delete() with the breaker tripped: error = %v, want ErrUnavailable", err)
	}
	if spy.deleteCalls != 0 {
		t.Errorf("Delete() reached the wrapped repository %d times while the breaker was open, want 0", spy.deleteCalls)
	}
}

func TestPostgresBreakerRepository_ClosedCircuit_ReachesNext(t *testing.T) {
	spy := &spyRepository{deleteErr: ErrNotFound}
	b := newTestBreaker(t)

	repo := NewBreakerRepository(spy, b, dbTestRetryAfter)

	err := repo.Delete(context.Background(), "key", "user-1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete() error = %v, want ErrNotFound (unchanged from the wrapped repository)", err)
	}
	if spy.deleteCalls != 1 {
		t.Errorf("Delete() reached the wrapped repository %d times, want 1", spy.deleteCalls)
	}
}

// TestPostgresBreakerRepository_ErrUnavailable_CarriesRetryAfter proves
// this decorator's rejection reuses the exact same ErrUnavailable/
// unavailableError the BlobStore breaker in s3_breaker.go produces —
// Handler needs exactly one case for both, which is the whole point of
// sharing the type (see s3_breaker.go's own doc comment on
// unavailableError).
func TestPostgresBreakerRepository_ErrUnavailable_CarriesRetryAfter(t *testing.T) {
	spy := &spyRepository{}
	b := newTestBreaker(t)
	b.Trip(context.Background())

	repo := NewBreakerRepository(spy, b, dbTestRetryAfter)

	err := repo.Delete(context.Background(), "key", "user-1")
	var withRetryAfter interface{ RetryAfter() time.Duration }
	if !errors.As(err, &withRetryAfter) {
		t.Fatalf("errors.As() found no RetryAfter() on %v", err)
	}
	if got := withRetryAfter.RetryAfter(); got != dbTestRetryAfter {
		t.Errorf("RetryAfter() = %v, want %v", got, dbTestRetryAfter)
	}
}
