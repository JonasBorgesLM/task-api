package main

import (
	"context"
	"expvar"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JonasBorgesLM/bastion"

	"github.com/JonasBorgesLM/task-api/internal/platform/pgerr"
)

// dbBreakerOpenTimeout is how long the shared PostgreSQL breaker stays
// Open before admitting a probe call (FR-01), and the exact value
// task.ErrUnavailable/user.ErrUnavailable/attachment.ErrUnavailable's
// Retry-After is derived from — see docs/DECISIONS.md § "Breaker
// compartilhado no PostgreSQL" for why each domain package is handed
// this value explicitly rather than owning its own copy: the breaker
// itself is shared across three packages, and none of them is the
// source of truth for its configuration.
const dbBreakerOpenTimeout = 30 * time.Second

// currentDBBreaker is what the postgres_breaker entry published by
// publishDBBreakerExpvarOnce reads from — same sync.Once + atomic.Pointer
// shape as currentCrierInstance and the attachment breaker atomics, for
// the same reason (see cmd/api/crier.go, cmd/api/attachment_breaker.go).
var (
	dbBreakerExpvarOnce sync.Once
	currentDBBreaker    atomic.Pointer[bastion.Breaker]
)

// publishDBBreakerExpvarOnce registers the postgres_breaker entry the
// first time it is called, and does nothing after. Safe to call whether
// or not a database is actually configured: with nothing stored in
// currentDBBreaker, the published value reports "disabled".
//
// Deliberately not wired into GET /health/ready — same argument as
// docs/DECISIONS.md § "Counts() no /debug/vars" makes for the attachment
// breakers: an open circuit here is a handled failure (a queue turned
// into a fast rejection), not a reason to stop routing traffic to this
// instance. Readiness keeps pinging *sql.DB directly (registerReadinessRoute),
// deliberately bypassing this abstraction — see docs/DECISIONS.md for
// why that inconsistency is intentional and must stay.
func publishDBBreakerExpvarOnce() {
	dbBreakerExpvarOnce.Do(func() {
		expvar.Publish("postgres_breaker", expvar.Func(func() any {
			return attachmentBreakerSnapshot(currentDBBreaker.Load())
		}))
	})
}

// dbBreakerHooks builds the bastion.Hooks passed to buildDBBreaker —
// same shape as attachmentBreakerHooks, see its doc comment for why
// OnStateChange and OnCall are both needed rather than OnStateChange
// alone.
func dbBreakerHooks(logger *slog.Logger) bastion.Hooks {
	return bastion.Hooks{
		OnStateChange: func(ctx context.Context, ev bastion.StateChangeEvent) {
			level := slog.LevelInfo
			if ev.To == bastion.StateOpen {
				level = slog.LevelWarn
			}
			logger.Log(ctx, level, "postgres circuit breaker state change",
				"breaker", ev.Name,
				"from", ev.From.String(),
				"to", ev.To.String(),
				"manual", ev.Manual,
			)
		},
		OnCall: func(ctx context.Context, ev bastion.CallEvent) {
			if !ev.Counted || ev.Err == nil {
				return
			}
			logger.Warn("postgres call failed",
				"breaker", ev.Name,
				"state", ev.State.String(),
				"error", ev.Err,
			)
		},
	}
}

// buildDBBreaker returns one bastion.Breaker shared by task, user and
// attachment's own Repository decorators — see docs/DECISIONS.md §
// "Breaker compartilhado no PostgreSQL" for why one breaker, not three:
// all three domains share the exact same *sql.DB connection pool, so
// pool exhaustion is one failure on one resource, not three independent
// ones.
//
// The classifier is internal/platform/pgerr.IsInfrastructureFailure,
// unchanged — the same predicate 16.A2 wrote for wrapDBError, reused
// directly rather than redefined, per issue #286's own instruction.
func buildDBBreaker(logger *slog.Logger) (*bastion.Breaker, error) {
	return bastion.New("postgres",
		bastion.WithIsFailure(pgerr.IsInfrastructureFailure),
		bastion.WithOpenTimeout(dbBreakerOpenTimeout),
		bastion.WithHooks(dbBreakerHooks(logger)),
	)
}
