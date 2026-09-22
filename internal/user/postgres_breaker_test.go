package user

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JonasBorgesLM/bastion"
)

// newTestBreaker mirrors internal/task/postgres_breaker_test.go's own
// helper — a low threshold so a handful of failures is enough to open
// it, and Trip is available for tests that need Open without any.
func newTestBreaker(t *testing.T) *bastion.Breaker {
	t.Helper()
	b, err := bastion.New("test", bastion.WithFailureThreshold(2))
	if err != nil {
		t.Fatalf("bastion.New() unexpected error: %v", err)
	}
	return b
}

// dbTestRetryAfter stands in for the value cmd/api would pass in
// production (dbBreakerOpenTimeout) — see
// internal/task/postgres_breaker_test.go's identical constant.
const dbTestRetryAfter = 17 * time.Second

// TestBreakerRepository_TrippedCircuit_NeverReachesNext is 16.C2's
// central acceptance criterion, for the transaction this package owns:
// CreateSession (BeginTx -> pg_advisory_xact_lock -> INSERT -> DELETE ->
// Commit in postgresRepository). Proven directly — fakeRepository's
// createSessionCalledWith stays at its zero value — rather than
// inferred from the error alone. maxSessions=5 here specifically so a
// leftover zero value cannot be confused with "was called with 0".
func TestBreakerRepository_TrippedCircuit_NeverReachesNext(t *testing.T) {
	fake := &fakeRepository{}
	b := newTestBreaker(t)
	b.Trip(context.Background())

	repo := NewBreakerRepository(fake, b, dbTestRetryAfter)

	err := repo.CreateSession(context.Background(), Session{}, 5)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("CreateSession() with the breaker tripped: error = %v, want ErrUnavailable", err)
	}
	if fake.createSessionCalledWith != 0 {
		t.Error("CreateSession() reached the wrapped repository while the breaker was open — a rejection must never let a transaction begin")
	}
}

// TestBreakerRepository_ClosedCircuit_ReachesNext is the contract's
// other half: an admitted call must still reach the wrapped repository
// and return its own domain errors unchanged.
func TestBreakerRepository_ClosedCircuit_ReachesNext(t *testing.T) {
	fake := &fakeRepository{createSessionErr: ErrAlreadyExists}
	b := newTestBreaker(t)

	repo := NewBreakerRepository(fake, b, dbTestRetryAfter)

	err := repo.CreateSession(context.Background(), Session{}, 5)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("CreateSession() error = %v, want ErrAlreadyExists (unchanged from the wrapped repository)", err)
	}
	if fake.createSessionCalledWith != 5 {
		t.Errorf("CreateSession() called wrapped repository with maxSessions=%d, want 5", fake.createSessionCalledWith)
	}
}

// TestBreakerRepository_ErrUnavailable_CarriesRetryAfter mirrors
// internal/task/postgres_breaker_test.go's identical test.
func TestBreakerRepository_ErrUnavailable_CarriesRetryAfter(t *testing.T) {
	fake := &fakeRepository{}
	b := newTestBreaker(t)
	b.Trip(context.Background())

	repo := NewBreakerRepository(fake, b, dbTestRetryAfter)

	err := repo.CreateSession(context.Background(), Session{}, 5)
	var withRetryAfter interface{ RetryAfter() time.Duration }
	if !errors.As(err, &withRetryAfter) {
		t.Fatalf("errors.As() found no RetryAfter() on %v", err)
	}
	if got := withRetryAfter.RetryAfter(); got != dbTestRetryAfter {
		t.Errorf("RetryAfter() = %v, want %v", got, dbTestRetryAfter)
	}
}

// raceRepository is a Repository double with no mutable state a
// concurrent call could race on, for the -race test below — see
// internal/task/postgres_breaker_test.go's identical type.
type raceRepository struct {
	calls atomic.Int64
}

func (r *raceRepository) CreateUser(context.Context, User) error { r.calls.Add(1); return nil }
func (r *raceRepository) FindUserByEmail(context.Context, string) (User, error) {
	r.calls.Add(1)
	return User{}, nil
}
func (r *raceRepository) FindUserByID(context.Context, string) (User, error) {
	r.calls.Add(1)
	return User{}, nil
}
func (r *raceRepository) UpdateUserPassword(context.Context, string, string) error {
	r.calls.Add(1)
	return nil
}
func (r *raceRepository) DeleteUser(context.Context, string) error { r.calls.Add(1); return nil }
func (r *raceRepository) CreateSession(context.Context, Session, int) error {
	r.calls.Add(1)
	return nil
}
func (r *raceRepository) FindSessionByTokenHash(context.Context, string) (Session, error) {
	r.calls.Add(1)
	return Session{}, nil
}
func (r *raceRepository) DeleteSession(context.Context, string) error { r.calls.Add(1); return nil }
func (r *raceRepository) FindSessionsForUser(context.Context, string) ([]Session, error) {
	r.calls.Add(1)
	return nil, nil
}
func (r *raceRepository) DeleteSessionsForUser(context.Context, string) error {
	r.calls.Add(1)
	return nil
}
func (r *raceRepository) DeleteSessionsForUserExcept(context.Context, string, string) error {
	r.calls.Add(1)
	return nil
}
func (r *raceRepository) DeleteExpiredSessions(context.Context, time.Time) error {
	r.calls.Add(1)
	return nil
}

// TestBreakerRepository_CreateSession_ConcurrentCallsAreRaceFree is
// 16.C2's "-race" acceptance criterion for the locked transaction this
// package owns.
func TestBreakerRepository_CreateSession_ConcurrentCallsAreRaceFree(t *testing.T) {
	next := &raceRepository{}
	b := newTestBreaker(t)
	repo := NewBreakerRepository(next, b, dbTestRetryAfter)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_ = repo.CreateSession(context.Background(), Session{}, 5)
		}()
	}
	wg.Wait()

	if got := next.calls.Load(); got != goroutines {
		t.Errorf("wrapped CreateSession reached %d times, want %d", got, goroutines)
	}
}
