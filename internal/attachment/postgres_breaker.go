package attachment

import (
	"context"
	"time"

	"github.com/JonasBorgesLM/bastion"
)

// breakerRepository decorates a Repository with a circuit breaker shared
// across every domain backed by the same PostgreSQL pool (task, user,
// attachment) — see docs/DECISIONS.md § "Breaker compartilhado no
// PostgreSQL" and internal/task/postgres_breaker.go's identical doc
// comment for the full reasoning.
//
// Reuses this package's own ErrUnavailable and unavailableError (see
// s3_breaker.go) rather than defining a second pair: a caller of this
// package — Service, Handler — never needs to know whether a rejection
// came from the S3 breaker or this one, only that some breaker said no
// and for how long to wait. Both breakers translate to the exact same
// sentinel for exactly that reason.
//
// One Execute per method, the method's own body and all — never a
// statement inside it, the same rule
// internal/task/postgres_breaker.go's Update and
// internal/user/postgres_breaker.go's CreateSession exist to uphold.
// This package's own postgresRepository has no multi-statement
// transaction among its methods, but the rule is applied uniformly
// regardless — decorating at the Repository interface makes a per-
// statement breaker structurally impossible here, not just avoided by
// convention.
type breakerRepository struct {
	next       Repository
	b          *bastion.Breaker
	retryAfter time.Duration
}

// NewBreakerRepository wraps next with b, a breaker built and owned by
// the caller (cmd/api) — see internal/task/postgres_breaker.go's
// identical constructor doc comment for why this package never builds
// its own.
func NewBreakerRepository(next Repository, b *bastion.Breaker, retryAfter time.Duration) Repository {
	return &breakerRepository{next: next, b: b, retryAfter: retryAfter}
}

func (r *breakerRepository) Create(ctx context.Context, attachment Attachment, userID string) error {
	return translateBreakerError(bastion.Do(ctx, r.b, func(ctx context.Context) error {
		return r.next.Create(ctx, attachment, userID)
	}), r.retryAfter)
}

func (r *breakerRepository) FindByStorageKey(ctx context.Context, storageKey, userID string) (Attachment, error) {
	a, err := bastion.Execute(ctx, r.b, func(ctx context.Context) (Attachment, error) {
		return r.next.FindByStorageKey(ctx, storageKey, userID)
	})
	return a, translateBreakerError(err, r.retryAfter)
}

func (r *breakerRepository) FindByTask(ctx context.Context, taskID, userID string) ([]Attachment, error) {
	attachments, err := bastion.Execute(ctx, r.b, func(ctx context.Context) ([]Attachment, error) {
		return r.next.FindByTask(ctx, taskID, userID)
	})
	return attachments, translateBreakerError(err, r.retryAfter)
}

func (r *breakerRepository) Delete(ctx context.Context, storageKey, userID string) error {
	return translateBreakerError(bastion.Do(ctx, r.b, func(ctx context.Context) error {
		return r.next.Delete(ctx, storageKey, userID)
	}), r.retryAfter)
}

func (r *breakerRepository) TotalBytesForUser(ctx context.Context, userID string) (int64, error) {
	total, err := bastion.Execute(ctx, r.b, func(ctx context.Context) (int64, error) {
		return r.next.TotalBytesForUser(ctx, userID)
	})
	return total, translateBreakerError(err, r.retryAfter)
}

func (r *breakerRepository) CountByTask(ctx context.Context, taskID, userID string) (int, error) {
	n, err := bastion.Execute(ctx, r.b, func(ctx context.Context) (int, error) {
		return r.next.CountByTask(ctx, taskID, userID)
	})
	return n, translateBreakerError(err, r.retryAfter)
}

func (r *breakerRepository) UnreferencedKeys(ctx context.Context, keys []string) ([]string, error) {
	unreferenced, err := bastion.Execute(ctx, r.b, func(ctx context.Context) ([]string, error) {
		return r.next.UnreferencedKeys(ctx, keys)
	})
	return unreferenced, translateBreakerError(err, r.retryAfter)
}
