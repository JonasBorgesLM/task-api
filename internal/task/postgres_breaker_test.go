package task

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JonasBorgesLM/bastion"
)

// newTestBreaker returns a *bastion.Breaker suitable for driving
// directly from a test — Trip/Reset, no hooks, a low threshold so a
// handful of failures is enough to open it without a long loop.
func newTestBreaker(t *testing.T) *bastion.Breaker {
	t.Helper()
	b, err := bastion.New("test", bastion.WithFailureThreshold(2))
	if err != nil {
		t.Fatalf("bastion.New() unexpected error: %v", err)
	}
	return b
}

// TestBreakerRepository_TrippedCircuit_NeverReachesNext is 16.C2's
// central acceptance criterion: a rejection must never happen with a
// transaction open, because next.Update is never invoked at all when
// the breaker refuses the call. Proven directly — fakeRepository.
// updateCalled stays false — rather than inferred from the error alone,
// since a negative assertion on the error ("got ErrUnavailable") would
// be satisfied even if next had somehow been called too.
//
// Update is the transactional method (BeginTx -> SELECT ... FOR UPDATE
// -> UPDATE -> Commit in postgresRepository) that motivated the "whole
// method, never a statement" rule in the first place — see
// postgres_breaker.go's own doc comment.
func TestBreakerRepository_TrippedCircuit_NeverReachesNext(t *testing.T) {
	fake := &fakeRepository{}
	b := newTestBreaker(t)
	b.Trip(context.Background())

	repo := NewBreakerRepository(fake, b, dbTestRetryAfter)

	err := repo.Update(context.Background(), newFakeTask(StatusPending))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Update() with the breaker tripped: error = %v, want ErrUnavailable", err)
	}
	if fake.updateCalled {
		t.Error("Update() reached the wrapped repository while the breaker was open — a rejection must never let a transaction begin")
	}
}

// TestBreakerRepository_ClosedCircuit_ReachesNext is the contract's
// other half: an admitted call must still reach the wrapped repository
// and return its own domain errors unchanged, not swallowed or
// retranslated into ErrUnavailable.
func TestBreakerRepository_ClosedCircuit_ReachesNext(t *testing.T) {
	fake := &fakeRepository{updateErr: ErrConflict}
	b := newTestBreaker(t)

	repo := NewBreakerRepository(fake, b, dbTestRetryAfter)

	err := repo.Update(context.Background(), newFakeTask(StatusPending))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Update() error = %v, want ErrConflict (unchanged from the wrapped repository)", err)
	}
	if !fake.updateCalled {
		t.Error("Update() never reached the wrapped repository, want it to have been called")
	}
}

// dbTestRetryAfter stands in for the value cmd/api would pass in
// production (dbBreakerOpenTimeout) — this package has no reason to
// know that number, only that whatever value the caller gives
// NewBreakerRepository is exactly what comes back on the RetryAfter()
// side of an ErrUnavailable.
const dbTestRetryAfter = 17 * time.Second

// TestBreakerRepository_ErrUnavailable_CarriesRetryAfter pins the same
// Handler-facing contract internal/attachment/s3_breaker.go's own test
// pins for the S3 breaker: errors.As against
// interface{ RetryAfter() time.Duration } must find the exact value the
// decorator was constructed with.
func TestBreakerRepository_ErrUnavailable_CarriesRetryAfter(t *testing.T) {
	fake := &fakeRepository{}
	b := newTestBreaker(t)
	b.Trip(context.Background())

	repo := NewBreakerRepository(fake, b, dbTestRetryAfter)

	err := repo.Update(context.Background(), newFakeTask(StatusPending))
	var withRetryAfter interface{ RetryAfter() time.Duration }
	if !errors.As(err, &withRetryAfter) {
		t.Fatalf("errors.As() found no RetryAfter() on %v", err)
	}
	if got := withRetryAfter.RetryAfter(); got != dbTestRetryAfter {
		t.Errorf("RetryAfter() = %v, want %v", got, dbTestRetryAfter)
	}
}

// raceRepository is a Repository double with no mutable state a
// concurrent call could race on — every method is a pure function of
// its arguments. Used only by the -race test below, deliberately
// separate from fakeRepository (whose call-recording fields are not
// safe for concurrent use, by design: those tests are single-goroutine).
type raceRepository struct {
	calls atomic.Int64
}

func (r *raceRepository) Create(context.Context, Task) error { r.calls.Add(1); return nil }
func (r *raceRepository) FindByID(context.Context, string, string) (Task, error) {
	r.calls.Add(1)
	return Task{}, nil
}
func (r *raceRepository) FindAll(context.Context, string, int, int, []Status, []Priority) ([]Task, error) {
	r.calls.Add(1)
	return nil, nil
}
func (r *raceRepository) Update(context.Context, Task) error { r.calls.Add(1); return nil }
func (r *raceRepository) Delete(context.Context, string, string) error {
	r.calls.Add(1)
	return nil
}
func (r *raceRepository) CountAll(context.Context, string, []Status, []Priority) (int, error) {
	r.calls.Add(1)
	return 0, nil
}
func (r *raceRepository) CountByStatusAndPriority(context.Context, string, []Status, []Priority) (map[Status]int, map[Priority]int, error) {
	r.calls.Add(1)
	return nil, nil, nil
}

// TestBreakerRepository_Update_ConcurrentCallsAreRaceFree is 16.C2's
// "-race" acceptance criterion for the locked transaction this package
// owns (Update). Run with `go test -race`; a data race here would be in
// the decorator or in bastion.Breaker itself, since raceRepository has
// no shared mutable state of its own beyond the atomic counter.
func TestBreakerRepository_Update_ConcurrentCallsAreRaceFree(t *testing.T) {
	next := &raceRepository{}
	b := newTestBreaker(t)
	repo := NewBreakerRepository(next, b, dbTestRetryAfter)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_ = repo.Update(context.Background(), newFakeTask(StatusPending))
		}()
	}
	wg.Wait()

	if got := next.calls.Load(); got != goroutines {
		t.Errorf("wrapped Update reached %d times, want %d", got, goroutines)
	}
}
