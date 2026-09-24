package main

import (
	"testing"
	"time"

	"github.com/JonasBorgesLM/task-api/internal/config"
	"github.com/JonasBorgesLM/task-api/internal/task"
)

// TestBuildCachedTaskRepository_RedisAddrEmpty_ReturnsNextUnwrapped is
// buildCachedTaskRepository's "absence means the feature does not exist"
// contract: with no REDIS_ADDR, the function must not just "work" — it must
// hand back the exact Repository it was given, not a CachedRepository
// wrapping it, the same identity newServer relies on for every existing
// test in this package (none of which sets RedisAddr, per testConfig).
func TestBuildCachedTaskRepository_RedisAddrEmpty_ReturnsNextUnwrapped(t *testing.T) {
	next := task.NewMemoryRepository()

	got, closeCache, err := buildCachedTaskRepository(config.Config{}, discardLogger(), next)
	if err != nil {
		t.Fatalf("buildCachedTaskRepository() unexpected error: %v", err)
	}
	if got != task.Repository(next) {
		t.Error("buildCachedTaskRepository() with empty RedisAddr did not return next unchanged")
	}
	if err := closeCache(); err != nil {
		t.Errorf("closeCache() unexpected error: %v", err)
	}
}

// TestBuildCachedTaskRepository_RedisAddrSet_WrapsInCachedRepository is the
// other half: a configured RedisAddr must produce a *task.CachedRepository,
// and — since cistern's reads fail open — must succeed even against an
// address nothing listens on. Connectivity is a request-time concern (see
// buildCachedTaskRepository's own doc comment on why there is no startup
// ping), not a construction-time one.
func TestBuildCachedTaskRepository_RedisAddrSet_WrapsInCachedRepository(t *testing.T) {
	next := task.NewMemoryRepository()
	cfg := config.Config{
		RedisAddr:       "127.0.0.1:1", // nothing listens here
		RedisCacheTTL:   time.Minute,
		RedisCacheL1TTL: 5 * time.Second,
	}

	got, closeCache, err := buildCachedTaskRepository(cfg, discardLogger(), next)
	if err != nil {
		t.Fatalf("buildCachedTaskRepository() unexpected error: %v", err)
	}
	if _, ok := got.(*task.CachedRepository); !ok {
		t.Errorf("buildCachedTaskRepository() with RedisAddr set returned %T, want *task.CachedRepository", got)
	}
	if err := closeCache(); err != nil {
		t.Errorf("closeCache() unexpected error: %v", err)
	}
}
