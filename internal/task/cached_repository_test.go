package task

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/bus"
	"github.com/JonasBorgesLM/cistern/memory"
	"github.com/JonasBorgesLM/cistern/redisstore"
	"github.com/redis/go-redis/v9"
)

// newTestCachedRepository builds a CachedRepository backed by an L1-only
// cistern cache (real memory.Store — pure Go, no external service, the same
// reason internal/user/token_cache_test.go needs nothing external either).
// ttl is generous enough that none of this file's tests race their own
// cache's expiry; tests that need TTL expiry itself use a short one directly.
func newTestCachedRepository(t *testing.T, next Repository, ttl time.Duration) *CachedRepository {
	t.Helper()
	l1, err := memory.New()
	if err != nil {
		t.Fatalf("memory.New() unexpected error: %v", err)
	}
	repo, err := NewCachedRepository(next, cistern.WithL1(l1), cistern.WithTTL(ttl))
	if err != nil {
		t.Fatalf("NewCachedRepository() unexpected error: %v", err)
	}
	return repo
}

// countingRepository is a Repository double that only counts calls and
// returns whatever *Result/*Err fields say — no cache-aware behavior of its
// own. Used as `next` across this file's tests, the same role fakeRepository
// plays for Service's tests, kept separate because CachedRepository's tests
// need per-method call counts (to prove a cache hit skipped next, or a write
// invalidated it), which fakeRepository does not track.
type countingRepository struct {
	createCalls, findByIDCalls, findAllCalls, updateCalls, deleteCalls int
	countAllCalls, countByStatusAndPriorityCalls                       int

	createErr, findByIDErr, findAllErr, updateErr, deleteErr error
	countAllErr, countByStatusAndPriorityErr                 error

	findByIDResult        Task
	findAllResult         []Task
	countAllResult        int
	countByStatusResult   map[Status]int
	countByPriorityResult map[Priority]int
}

func (r *countingRepository) Create(context.Context, Task) error {
	r.createCalls++
	return r.createErr
}

func (r *countingRepository) FindByID(context.Context, string, string) (Task, error) {
	r.findByIDCalls++
	return r.findByIDResult, r.findByIDErr
}

func (r *countingRepository) FindAll(context.Context, string, int, int, []Status, []Priority) ([]Task, error) {
	r.findAllCalls++
	return r.findAllResult, r.findAllErr
}

func (r *countingRepository) Update(context.Context, Task) error {
	r.updateCalls++
	return r.updateErr
}

func (r *countingRepository) Delete(context.Context, string, string) error {
	r.deleteCalls++
	return r.deleteErr
}

func (r *countingRepository) CountAll(context.Context, string, []Status, []Priority) (int, error) {
	r.countAllCalls++
	return r.countAllResult, r.countAllErr
}

func (r *countingRepository) CountByStatusAndPriority(context.Context, string, []Status, []Priority) (map[Status]int, map[Priority]int, error) {
	r.countByStatusAndPriorityCalls++
	return r.countByStatusResult, r.countByPriorityResult, r.countByStatusAndPriorityErr
}

// TestNewCachedRepository_DelegatesEveryMethodToNext is CI-1's acceptance
// criterion: before any caching logic exists, the decorator must already be a
// complete, behavior-preserving Repository — every method reaches next
// exactly once and returns its result unchanged.
func TestNewCachedRepository_DelegatesEveryMethodToNext(t *testing.T) {
	wantErr := errors.New("next failed")
	next := &countingRepository{
		findByIDResult:              newFakeTask(StatusPending),
		findAllResult:               []Task{newFakeTask(StatusDone)},
		countAllResult:              3,
		countByStatusResult:         map[Status]int{StatusPending: 1},
		createErr:                   wantErr,
		findByIDErr:                 wantErr,
		findAllErr:                  wantErr,
		updateErr:                   wantErr,
		deleteErr:                   wantErr,
		countAllErr:                 wantErr,
		countByStatusAndPriorityErr: wantErr,
	}
	repo := newTestCachedRepository(t, next, time.Minute)
	ctx := context.Background()

	if err := repo.Create(ctx, newFakeTask(StatusPending)); !errors.Is(err, wantErr) {
		t.Errorf("Create() error = %v, want %v", err, wantErr)
	}
	if _, err := repo.FindByID(ctx, "id", "user"); !errors.Is(err, wantErr) {
		t.Errorf("FindByID() error = %v, want %v", err, wantErr)
	}
	if _, err := repo.FindAll(ctx, "user", 10, 0, nil, nil); !errors.Is(err, wantErr) {
		t.Errorf("FindAll() error = %v, want %v", err, wantErr)
	}
	if err := repo.Update(ctx, newFakeTask(StatusPending)); !errors.Is(err, wantErr) {
		t.Errorf("Update() error = %v, want %v", err, wantErr)
	}
	if err := repo.Delete(ctx, "id", "user"); !errors.Is(err, wantErr) {
		t.Errorf("Delete() error = %v, want %v", err, wantErr)
	}
	if _, err := repo.CountAll(ctx, "user", nil, nil); !errors.Is(err, wantErr) {
		t.Errorf("CountAll() error = %v, want %v", err, wantErr)
	}
	if _, _, err := repo.CountByStatusAndPriority(ctx, "user", nil, nil); !errors.Is(err, wantErr) {
		t.Errorf("CountByStatusAndPriority() error = %v, want %v", err, wantErr)
	}

	for name, got := range map[string]int{
		"Create":                   next.createCalls,
		"FindByID":                 next.findByIDCalls,
		"FindAll":                  next.findAllCalls,
		"Update":                   next.updateCalls,
		"Delete":                   next.deleteCalls,
		"CountAll":                 next.countAllCalls,
		"CountByStatusAndPriority": next.countByStatusAndPriorityCalls,
	} {
		if got != 1 {
			t.Errorf("next.%s called %d times, want exactly 1", name, got)
		}
	}
}

// --- FindAll caching ---

// TestCachedRepository_FindAll_SecondCallWithSameParamsSkipsNext is this
// decorator's whole point: a repeated read for the same userID/filters/page
// must not reach next a second time.
func TestCachedRepository_FindAll_SecondCallWithSameParamsSkipsNext(t *testing.T) {
	next := &countingRepository{findAllResult: []Task{newFakeTask(StatusPending)}}
	repo := newTestCachedRepository(t, next, time.Minute)
	ctx := context.Background()

	if _, err := repo.FindAll(ctx, "user-1", 10, 0, nil, nil); err != nil {
		t.Fatalf("first FindAll() unexpected error: %v", err)
	}
	if _, err := repo.FindAll(ctx, "user-1", 10, 0, nil, nil); err != nil {
		t.Fatalf("second FindAll() unexpected error: %v", err)
	}

	if next.findAllCalls != 1 {
		t.Errorf("next.FindAll called %d times, want 1 (second call should have hit the cache)", next.findAllCalls)
	}
}

// TestCachedRepository_FindAll_DifferentFiltersAreDifferentCacheEntries pins
// docs/DECISIONS.md:1638's requirement directly: the cache key must cover
// statuses/priorities/limit/offset, not just userID, since there is no index
// behind those filters for the cache to be silently redundant with.
func TestCachedRepository_FindAll_DifferentFiltersAreDifferentCacheEntries(t *testing.T) {
	next := &countingRepository{findAllResult: []Task{newFakeTask(StatusPending)}}
	repo := newTestCachedRepository(t, next, time.Minute)
	ctx := context.Background()

	if _, err := repo.FindAll(ctx, "user-1", 10, 0, []Status{StatusPending}, nil); err != nil {
		t.Fatalf("FindAll(pending) unexpected error: %v", err)
	}
	if _, err := repo.FindAll(ctx, "user-1", 10, 0, []Status{StatusDone}, nil); err != nil {
		t.Fatalf("FindAll(done) unexpected error: %v", err)
	}

	if next.findAllCalls != 2 {
		t.Errorf("next.FindAll called %d times, want 2 (different status filters must not share a cache entry)", next.findAllCalls)
	}
}

// TestCachedRepository_FindAll_KeyOrderInsensitiveToFilterOrder proves the
// cache key normalizes filter order: Service.ListTasks deduplicates
// statuses/priorities but does not guarantee a stable order, and two
// requests for the same *set* of filters should still be one cache entry.
func TestCachedRepository_FindAll_KeyOrderInsensitiveToFilterOrder(t *testing.T) {
	next := &countingRepository{findAllResult: []Task{newFakeTask(StatusPending)}}
	repo := newTestCachedRepository(t, next, time.Minute)
	ctx := context.Background()

	if _, err := repo.FindAll(ctx, "user-1", 10, 0, []Status{StatusDone, StatusPending}, nil); err != nil {
		t.Fatalf("FindAll([done,pending]) unexpected error: %v", err)
	}
	if _, err := repo.FindAll(ctx, "user-1", 10, 0, []Status{StatusPending, StatusDone}, nil); err != nil {
		t.Fatalf("FindAll([pending,done]) unexpected error: %v", err)
	}

	if next.findAllCalls != 1 {
		t.Errorf("next.FindAll called %d times, want 1 (same filter set in a different order must hit the cache)", next.findAllCalls)
	}
}

// TestCachedRepository_CountAll_NeverCached protects the invariant behind
// GET /v1/tasks's X-Total-Count: it must always be a fresh count, never a
// value the decorator answered from its own cache.
func TestCachedRepository_CountAll_NeverCached(t *testing.T) {
	next := &countingRepository{countAllResult: 5}
	repo := newTestCachedRepository(t, next, time.Minute)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := repo.CountAll(ctx, "user-1", nil, nil); err != nil {
			t.Fatalf("CountAll() call %d unexpected error: %v", i+1, err)
		}
	}

	if next.countAllCalls != 3 {
		t.Errorf("next.CountAll called %d times, want 3 (CountAll must never be served from cache)", next.countAllCalls)
	}
}

// TestCachedRepository_FindAll_EntryExpires_AfterTTL uses a real short sleep,
// not an injectable clock: unlike internal/user/token_cache.go (this
// project's own type, free to take a `now func() time.Time`), cistern's
// memory.Store has no clock injection point (memory.Option is
// WithMaxBytes/WithMaxEntries/WithOnEvict only) — a real wait is the only way
// to observe its TTL from outside the library.
func TestCachedRepository_FindAll_EntryExpires_AfterTTL(t *testing.T) {
	const ttl = 20 * time.Millisecond
	next := &countingRepository{findAllResult: []Task{newFakeTask(StatusPending)}}
	repo := newTestCachedRepository(t, next, ttl)
	ctx := context.Background()

	if _, err := repo.FindAll(ctx, "user-1", 10, 0, nil, nil); err != nil {
		t.Fatalf("first FindAll() unexpected error: %v", err)
	}

	time.Sleep(3 * ttl)

	if _, err := repo.FindAll(ctx, "user-1", 10, 0, nil, nil); err != nil {
		t.Fatalf("FindAll() after TTL expiry unexpected error: %v", err)
	}

	if next.findAllCalls != 2 {
		t.Errorf("next.FindAll called %d times, want 2 (the entry should have expired and reached next again)", next.findAllCalls)
	}
}

// TestCachedRepository_FindAll_L2Unreachable_FallsBackToNext is this
// package's proof of "Redis down ≠ app down" (cistern's own README
// Properties table, ADR-0002): with only an unreachable L2 configured (no
// L1, so every call must actually reach it), FindAll still returns next's
// data instead of erroring. This needs no live Redis — it tests exactly the
// case where one is absent — so it runs in the default unit suite rather
// than behind TEST_REDIS_ADDR/integration like
// cached_repository_redis_test.go's cross-replica test does.
func TestCachedRepository_FindAll_L2Unreachable_FallsBackToNext(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr:                  "127.0.0.1:1", // nothing listens here
		DialTimeout:           200 * time.Millisecond,
		ContextTimeoutEnabled: true,
	})
	t.Cleanup(func() { client.Close() })

	l2, err := redisstore.New(client, redisstore.WithTimeout(200*time.Millisecond))
	if err != nil {
		t.Fatalf("redisstore.New() unexpected error: %v", err)
	}

	next := &countingRepository{findAllResult: []Task{newFakeTask(StatusPending)}}
	repo, err := NewCachedRepository(next, cistern.WithL2(l2), cistern.WithTTL(time.Minute))
	if err != nil {
		t.Fatalf("NewCachedRepository() unexpected error: %v", err)
	}

	tasks, err := repo.FindAll(context.Background(), "user-1", 10, 0, nil, nil)
	if err != nil {
		t.Fatalf("FindAll() with L2 unreachable: error = %v, want nil (fail-open)", err)
	}
	if len(tasks) != 1 {
		t.Errorf("FindAll() with L2 unreachable returned %d tasks, want next's 1 (fail-open must still serve real data)", len(tasks))
	}
	if next.findAllCalls != 1 {
		t.Errorf("next.FindAll called %d times, want 1", next.findAllCalls)
	}
}

// TestCachedRepository_FindAll_PreservesVersion pins the correctness this
// decorator's own doc comment on OnInvalidationError, and docs/DECISIONS.md
// § "Cache-aside para GET /v1/tasks"'s ETag paragraph, depend on: Task's
// Version field is `json:"-"` (never part of the public wire format — see
// task.go's own comment on it), so cistern's default JSON codec silently
// drops it on every encode. GetOrLoad round-trips its own value through the
// codec even on the call that first populates the cache (not just on a
// later hit), so this corrupts Version on every FindAll response once the
// cache is in the stack, not only on cache hits.
//
// The handler computes GET /v1/tasks's ETag from exactly (id, Version) of
// the rows FindAll returns — see internal task.go's pageETag and
// docs/DECISIONS.md § "ETag de GET /v1/tasks" — so a Version silently
// zeroed here makes two responses for the same task IDs share one ETag even
// after a real write changed one of those rows' Version, and a client
// polling with If-None-Match gets an incorrect 304 for content that
// actually changed.
func TestCachedRepository_FindAll_PreservesVersion(t *testing.T) {
	want := newFakeTask(StatusPending)
	want.Version = 42
	next := &countingRepository{findAllResult: []Task{want}}
	repo := newTestCachedRepository(t, next, time.Minute)

	tasks, err := repo.FindAll(context.Background(), testUserID, 10, 0, nil, nil)
	if err != nil {
		t.Fatalf("FindAll() unexpected error: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Version != 42 {
		t.Fatalf("FindAll()[0].Version = %+v, want Version 42 (next returned it; the cache must not lose it)", tasks)
	}
}

// --- Invalidation on write ---

// fakeBus is a bus.Bus double whose Publish return value and call count are
// both controlled by the test — the only way to deterministically force
// InvalidateTag to fail (memory.Store's own Bump does not fail in practice)
// and to prove a write attempted invalidation at all.
type fakeBus struct {
	publishErr   error
	publishCalls int
}

func (b *fakeBus) Publish(context.Context, bus.Event) error {
	b.publishCalls++
	return b.publishErr
}

func (b *fakeBus) Subscribe(bus.Handler) (func(), error) {
	return func() {}, nil
}

// newTestCachedRepositoryWithBus is newTestCachedRepository's sibling for
// the tests below, which need to observe InvalidateTag's own Publish calls
// rather than just cache hit/miss behavior.
func newTestCachedRepositoryWithBus(t *testing.T, next Repository, b *fakeBus) *CachedRepository {
	t.Helper()
	l1, err := memory.New()
	if err != nil {
		t.Fatalf("memory.New() unexpected error: %v", err)
	}
	repo, err := NewCachedRepository(next, cistern.WithL1(l1), cistern.WithBus(b), cistern.WithTTL(time.Minute))
	if err != nil {
		t.Fatalf("NewCachedRepository() unexpected error: %v", err)
	}
	return repo
}

// TestCachedRepository_Create_InvalidatesOwnersCachedLists proves
// invalidation actually evicts, not just that Create "still works": a page
// cached before Create must be a miss (reach next again) after it.
func TestCachedRepository_Create_InvalidatesOwnersCachedLists(t *testing.T) {
	next := &countingRepository{findAllResult: []Task{newFakeTask(StatusPending)}}
	repo := newTestCachedRepositoryWithBus(t, next, &fakeBus{})
	ctx := context.Background()

	if _, err := repo.FindAll(ctx, testUserID, 10, 0, nil, nil); err != nil {
		t.Fatalf("FindAll() before Create: unexpected error: %v", err)
	}
	if err := repo.Create(ctx, newFakeTask(StatusPending)); err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if _, err := repo.FindAll(ctx, testUserID, 10, 0, nil, nil); err != nil {
		t.Fatalf("FindAll() after Create: unexpected error: %v", err)
	}

	if next.findAllCalls != 2 {
		t.Errorf("next.FindAll called %d times, want 2 (Create should have invalidated the cached page)", next.findAllCalls)
	}
}

// TestCachedRepository_Update_InvalidatesOwnersCachedLists is
// TestCachedRepository_Create_InvalidatesOwnersCachedLists's Update-shaped
// sibling.
func TestCachedRepository_Update_InvalidatesOwnersCachedLists(t *testing.T) {
	next := &countingRepository{findAllResult: []Task{newFakeTask(StatusPending)}}
	repo := newTestCachedRepositoryWithBus(t, next, &fakeBus{})
	ctx := context.Background()

	if _, err := repo.FindAll(ctx, testUserID, 10, 0, nil, nil); err != nil {
		t.Fatalf("FindAll() before Update: unexpected error: %v", err)
	}
	if err := repo.Update(ctx, newFakeTask(StatusDone)); err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
	if _, err := repo.FindAll(ctx, testUserID, 10, 0, nil, nil); err != nil {
		t.Fatalf("FindAll() after Update: unexpected error: %v", err)
	}

	if next.findAllCalls != 2 {
		t.Errorf("next.FindAll called %d times, want 2 (Update should have invalidated the cached page)", next.findAllCalls)
	}
}

// TestCachedRepository_Delete_InvalidatesOwnersCachedLists is the same
// proof for Delete.
func TestCachedRepository_Delete_InvalidatesOwnersCachedLists(t *testing.T) {
	next := &countingRepository{findAllResult: []Task{newFakeTask(StatusPending)}}
	repo := newTestCachedRepositoryWithBus(t, next, &fakeBus{})
	ctx := context.Background()

	if _, err := repo.FindAll(ctx, testUserID, 10, 0, nil, nil); err != nil {
		t.Fatalf("FindAll() before Delete: unexpected error: %v", err)
	}
	if err := repo.Delete(ctx, "fake-id", testUserID); err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if _, err := repo.FindAll(ctx, testUserID, 10, 0, nil, nil); err != nil {
		t.Fatalf("FindAll() after Delete: unexpected error: %v", err)
	}

	if next.findAllCalls != 2 {
		t.Errorf("next.FindAll called %d times, want 2 (Delete should have invalidated the cached page)", next.findAllCalls)
	}
}

// TestCachedRepository_Create_InvalidationFailure_WriteStillSucceeds is this
// decorator's central fail-open guarantee on the write side: a Bus publish
// failure must never surface as Create's own error, only through
// OnInvalidationError — see that field's doc comment and docs/DECISIONS.md §
// "Cache-aside para GET /v1/tasks"'s correction paragraph for why.
func TestCachedRepository_Create_InvalidationFailure_WriteStillSucceeds(t *testing.T) {
	publishErr := errors.New("bus publish failed")
	next := &countingRepository{}
	repo := newTestCachedRepositoryWithBus(t, next, &fakeBus{publishErr: publishErr})

	var gotErr error
	var calls int
	repo.OnInvalidationError = func(err error) {
		calls++
		gotErr = err
	}

	if err := repo.Create(context.Background(), newFakeTask(StatusPending)); err != nil {
		t.Fatalf("Create() error = %v, want nil (invalidation failure must not fail the write)", err)
	}
	if calls != 1 {
		t.Fatalf("OnInvalidationError called %d times, want 1", calls)
	}
	if !errors.Is(gotErr, publishErr) {
		t.Errorf("OnInvalidationError got %v, want it to wrap %v", gotErr, publishErr)
	}
}

// TestCachedRepository_Create_NextFails_NeverInvalidates is the other half
// of the write contract: a write that never happened must not invalidate
// anything, so a concurrent reader's still-valid cache entry is left alone.
func TestCachedRepository_Create_NextFails_NeverInvalidates(t *testing.T) {
	next := &countingRepository{createErr: errors.New("insert failed")}
	b := &fakeBus{}
	repo := newTestCachedRepositoryWithBus(t, next, b)

	if err := repo.Create(context.Background(), newFakeTask(StatusPending)); err == nil {
		t.Fatal("Create() error = nil, want the underlying failure")
	}

	if b.publishCalls != 0 {
		t.Errorf("Bus.Publish called %d times, want 0 (a failed write must never invalidate)", b.publishCalls)
	}
}
