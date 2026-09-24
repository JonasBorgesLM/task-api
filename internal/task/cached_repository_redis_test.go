//go:build integration

package task

// Integration tests for CachedRepository against a real Redis instance —
// the "integration" build tag is what keeps them out of a plain
// `go test ./...`, the same separation postgres_repository_test.go uses for
// PostgreSQL. TestCachedRepository_FindAll_L2Unreachable_FallsBackToNext
// (cached_repository_test.go) tests the *absence* of Redis and needs none of
// this — only the properties that genuinely require two processes agreeing
// through a real Redis (cross-replica invalidation via Bus) belong here.
//
// To run locally:
//
//	docker compose up -d redis          # once k8s/25-redis.yaml's local
//	                                     # counterpart exists in
//	                                     # docker-compose.yml
//	TEST_REDIS_ADDR="localhost:6379" go test -tags=integration ./internal/task/... -run Redis_CachedRepository -v
//
// newRedisTestClient skips at runtime if TEST_REDIS_ADDR isn't set, the same
// second, independent safeguard newPostgresTestRepo uses.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/redisstore"
	"github.com/redis/go-redis/v9"
)

const testRedisAddrEnv = "TEST_REDIS_ADDR"

// newRedisTestClient returns a *redis.Client for TEST_REDIS_ADDR, skipping
// the test if it isn't set. t.Cleanup closes it — mirrors
// newPostgresTestRepo's shape in postgres_repository_test.go.
func newRedisTestClient(t *testing.T) *redis.Client {
	t.Helper()

	addr := os.Getenv(testRedisAddrEnv)
	if addr == "" {
		t.Skipf("%s not set; skipping Redis integration test (see docker-compose.yml)", testRedisAddrEnv)
	}

	client := redis.NewClient(&redis.Options{
		Addr:                  addr,
		ContextTimeoutEnabled: true,
	})
	t.Cleanup(func() { client.Close() })
	return client
}

// newRedisCachedRepository builds one "replica"'s CachedRepository, backed
// by client's Redis for both L2 and Bus, with no L1 shared between replicas
// (each gets its own, freshly built here) — the same shape two separate
// task-api processes would have in production.
func newRedisCachedRepository(t *testing.T, client *redis.Client, next Repository) *CachedRepository {
	t.Helper()

	l2, err := redisstore.New(client, redisstore.WithTimeout(2*time.Second))
	if err != nil {
		t.Fatalf("redisstore.New() unexpected error: %v", err)
	}
	b, err := redisstore.NewBus(client)
	if err != nil {
		t.Fatalf("redisstore.NewBus() unexpected error: %v", err)
	}
	repo, err := NewCachedRepository(next, cistern.WithL2(l2), cistern.WithBus(b), cistern.WithTTL(time.Minute))
	if err != nil {
		t.Fatalf("NewCachedRepository() unexpected error: %v", err)
	}
	return repo
}

// TestRedis_CachedRepository_CrossReplica_InvalidationPropagatesViaBus is
// docs/DECISIONS.md § "Cache-aside para GET /v1/tasks"'s central promise,
// proven end to end: two CachedRepository instances sharing one Redis, one
// per "replica", each with its own next so a hit never crosses processes by
// accident. A write through replicaB must invalidate what replicaA already
// cached — via the real Bus, not by replicaA's own TTL expiring.
//
// TestPostgres_* names postgres_repository_test.go's tests for the same
// reason: a readability convention, not a selector — the build tag is what
// actually excludes this file from a plain `go test ./...`.
func TestRedis_CachedRepository_CrossReplica_InvalidationPropagatesViaBus(t *testing.T) {
	client := newRedisTestClient(t)

	// A userID unique to this run, not the shared testUserID: the cache
	// entry this test writes lives in real Redis with a real TTL, which
	// outlives any single `go test` process — reusing testUserID would let
	// a previous run's still-live entry make this run's first read a hit
	// instead of the miss it needs to observe.
	userID := fmt.Sprintf("cross-replica-test-%d", time.Now().UnixNano())
	task := newFakeTask(StatusPending)
	task.UserID = userID

	nextA := &countingRepository{findAllResult: []Task{task}}
	replicaA := newRedisCachedRepository(t, client, nextA)

	nextB := &countingRepository{}
	replicaB := newRedisCachedRepository(t, client, nextB)

	ctx := context.Background()

	if _, err := replicaA.FindAll(ctx, userID, 10, 0, nil, nil); err != nil {
		t.Fatalf("replicaA.FindAll() before write: unexpected error: %v", err)
	}
	if _, err := replicaA.FindAll(ctx, userID, 10, 0, nil, nil); err != nil {
		t.Fatalf("replicaA.FindAll() second read: unexpected error: %v", err)
	}
	if nextA.findAllCalls != 1 {
		t.Fatalf("nextA.FindAll called %d times before the write, want 1 (page should already be cached)", nextA.findAllCalls)
	}

	if err := replicaB.Create(ctx, task); err != nil {
		t.Fatalf("replicaB.Create() unexpected error: %v", err)
	}

	// No sleep for an L1 TTL: the Bus event from replicaB's InvalidateTag
	// must already have reached replicaA's subscription by the time
	// Publish returns (delivery is synchronous, options.go's WithBus doc
	// comment) — a passing test here is specifically evidence the *Bus*
	// closed the gap, not a TTL expiring underneath it.
	if _, err := replicaA.FindAll(ctx, userID, 10, 0, nil, nil); err != nil {
		t.Fatalf("replicaA.FindAll() after replicaB's write: unexpected error: %v", err)
	}

	if nextA.findAllCalls != 2 {
		t.Errorf("nextA.FindAll called %d times after replicaB's write, want 2 (Bus should have invalidated replicaA's cached page)", nextA.findAllCalls)
	}
}
