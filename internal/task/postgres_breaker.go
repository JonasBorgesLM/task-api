package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/JonasBorgesLM/bastion"
)

// breakerRepository decorates a Repository with a circuit breaker shared
// across every domain backed by the same PostgreSQL pool (task, user,
// attachment) — see docs/DECISIONS.md § "Breaker compartilhado no
// PostgreSQL" for why one shared breaker, not one per domain: the
// failure this guards against is pool exhaustion on one *sql.DB, a
// single resource three independent circuits would each learn about
// separately and too slowly.
//
// This is the same decorator shape internal/attachment/s3_breaker.go
// uses: it also implements Repository, wraps another Repository, and is
// the only file in this package that imports bastion. Every sentinel it
// can return is this package's own (see translateBreakerError).
//
// One Execute per method, the method's own body and all — never a
// statement inside it. Update's transaction (BeginTx, SELECT ... FOR
// UPDATE, UPDATE, Commit) lives entirely inside next.Update, which the
// breaker either admits whole or never calls at all: a rejection can
// never land between BeginTx and Commit, because there is nothing
// running yet for it to land between.
type breakerRepository struct {
	next       Repository
	b          *bastion.Breaker
	retryAfter time.Duration
}

// NewBreakerRepository wraps next with b, a breaker built and owned by
// the caller (cmd/api) — this package never constructs its own, because
// b is shared with user and attachment's own decorators over the same
// underlying pool, and its classifier (internal/platform/pgerr.
// IsInfrastructureFailure, unchanged — see docs/DECISIONS.md § "Breaker
// compartilhado no PostgreSQL") is set once, at that shared
// construction, not by this package. retryAfter is the openTimeout b
// was built with; cmd/api passes it explicitly rather than this package
// assuming a value, since the breaker's configuration is a fact cmd/api
// owns, not this package.
func NewBreakerRepository(next Repository, b *bastion.Breaker, retryAfter time.Duration) Repository {
	return &breakerRepository{next: next, b: b, retryAfter: retryAfter}
}

func (r *breakerRepository) Create(ctx context.Context, task Task) error {
	return translateBreakerError(bastion.Do(ctx, r.b, func(ctx context.Context) error {
		return r.next.Create(ctx, task)
	}), r.retryAfter)
}

func (r *breakerRepository) FindByID(ctx context.Context, id, userID string) (Task, error) {
	t, err := bastion.Execute(ctx, r.b, func(ctx context.Context) (Task, error) {
		return r.next.FindByID(ctx, id, userID)
	})
	return t, translateBreakerError(err, r.retryAfter)
}

func (r *breakerRepository) FindAll(ctx context.Context, userID string, limit, offset int, statuses []Status, priorities []Priority) ([]Task, error) {
	tasks, err := bastion.Execute(ctx, r.b, func(ctx context.Context) ([]Task, error) {
		return r.next.FindAll(ctx, userID, limit, offset, statuses, priorities)
	})
	return tasks, translateBreakerError(err, r.retryAfter)
}

// Update is the method whose transaction motivated the "whole method,
// never a statement" rule — see this file's own doc comment.
func (r *breakerRepository) Update(ctx context.Context, task Task) error {
	return translateBreakerError(bastion.Do(ctx, r.b, func(ctx context.Context) error {
		return r.next.Update(ctx, task)
	}), r.retryAfter)
}

func (r *breakerRepository) Delete(ctx context.Context, id, userID string) error {
	return translateBreakerError(bastion.Do(ctx, r.b, func(ctx context.Context) error {
		return r.next.Delete(ctx, id, userID)
	}), r.retryAfter)
}

func (r *breakerRepository) CountAll(ctx context.Context, userID string, statuses []Status, priorities []Priority) (int, error) {
	n, err := bastion.Execute(ctx, r.b, func(ctx context.Context) (int, error) {
		return r.next.CountAll(ctx, userID, statuses, priorities)
	})
	return n, translateBreakerError(err, r.retryAfter)
}

// countByStatusAndPriorityResult exists only because bastion.Execute is
// generic over one result type, and CountByStatusAndPriority returns
// two maps.
type countByStatusAndPriorityResult struct {
	byStatus   map[Status]int
	byPriority map[Priority]int
}

func (r *breakerRepository) CountByStatusAndPriority(ctx context.Context, userID string, statuses []Status, priorities []Priority) (map[Status]int, map[Priority]int, error) {
	result, err := bastion.Execute(ctx, r.b, func(ctx context.Context) (countByStatusAndPriorityResult, error) {
		byStatus, byPriority, err := r.next.CountByStatusAndPriority(ctx, userID, statuses, priorities)
		return countByStatusAndPriorityResult{byStatus: byStatus, byPriority: byPriority}, err
	})
	return result.byStatus, result.byPriority, translateBreakerError(err, r.retryAfter)
}

// translateBreakerError maps bastion's own rejection sentinels to this
// package's ErrUnavailable at the boundary, so nothing above this file —
// Service, Handler, handleServiceError — ever needs bastion in scope.
// Every other error, including ErrDependencyUnavailable from
// postgresRepository's own wrapDBError, passes through unchanged: those
// were never bastion's to translate.
func translateBreakerError(err error, retryAfter time.Duration) error {
	if errors.Is(err, bastion.ErrOpenState) || errors.Is(err, bastion.ErrTooManyRequests) {
		return &unavailableError{cause: err, retryAfter: retryAfter}
	}
	return err
}

// unavailableError carries ErrUnavailable plus how long the caller
// should wait before retrying, without exposing the bastion sentinel it
// was built from through Unwrap/errors.As — the same shape
// internal/attachment/s3_breaker.go uses, package-local here because
// task and attachment do not share a common error type to reuse.
type unavailableError struct {
	cause      error
	retryAfter time.Duration
}

func (e *unavailableError) Error() string {
	return fmt.Sprintf("%s: %s", ErrUnavailable, e.cause)
}

// Is makes errors.Is(err, ErrUnavailable) succeed. Deliberately not an
// Unwrap — see internal/attachment/s3_breaker.go's identical method for
// why.
func (e *unavailableError) Is(target error) bool {
	return target == ErrUnavailable
}

// RetryAfter reports how long the caller should wait before retrying.
func (e *unavailableError) RetryAfter() time.Duration {
	return e.retryAfter
}
