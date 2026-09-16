package user

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
// PostgreSQL" and internal/task/postgres_breaker.go's identical doc
// comment for the full reasoning: one shared breaker, not one per
// domain, because pool exhaustion is one failure on one resource.
//
// One Execute per method, the method's own body and all — never a
// statement inside it. CreateSession's transaction (BeginTx,
// pg_advisory_xact_lock, INSERT, DELETE, Commit) lives entirely inside
// next.CreateSession, which the breaker either admits whole or never
// calls at all: a rejection can never land between BeginTx and Commit,
// because there is nothing running yet for it to land between.
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

func (r *breakerRepository) CreateUser(ctx context.Context, u User) error {
	return translateBreakerError(bastion.Do(ctx, r.b, func(ctx context.Context) error {
		return r.next.CreateUser(ctx, u)
	}), r.retryAfter)
}

func (r *breakerRepository) FindUserByEmail(ctx context.Context, email string) (User, error) {
	u, err := bastion.Execute(ctx, r.b, func(ctx context.Context) (User, error) {
		return r.next.FindUserByEmail(ctx, email)
	})
	return u, translateBreakerError(err, r.retryAfter)
}

func (r *breakerRepository) FindUserByID(ctx context.Context, id string) (User, error) {
	u, err := bastion.Execute(ctx, r.b, func(ctx context.Context) (User, error) {
		return r.next.FindUserByID(ctx, id)
	})
	return u, translateBreakerError(err, r.retryAfter)
}

func (r *breakerRepository) UpdateUserPassword(ctx context.Context, id, passwordHash string) error {
	return translateBreakerError(bastion.Do(ctx, r.b, func(ctx context.Context) error {
		return r.next.UpdateUserPassword(ctx, id, passwordHash)
	}), r.retryAfter)
}

func (r *breakerRepository) DeleteUser(ctx context.Context, id string) error {
	return translateBreakerError(bastion.Do(ctx, r.b, func(ctx context.Context) error {
		return r.next.DeleteUser(ctx, id)
	}), r.retryAfter)
}

// CreateSession is the method whose transaction motivated the "whole
// method, never a statement" rule — see this file's own doc comment.
func (r *breakerRepository) CreateSession(ctx context.Context, s Session, maxSessions int) error {
	return translateBreakerError(bastion.Do(ctx, r.b, func(ctx context.Context) error {
		return r.next.CreateSession(ctx, s, maxSessions)
	}), r.retryAfter)
}

func (r *breakerRepository) FindSessionByTokenHash(ctx context.Context, tokenHash string) (Session, error) {
	s, err := bastion.Execute(ctx, r.b, func(ctx context.Context) (Session, error) {
		return r.next.FindSessionByTokenHash(ctx, tokenHash)
	})
	return s, translateBreakerError(err, r.retryAfter)
}

func (r *breakerRepository) DeleteSession(ctx context.Context, tokenHash string) error {
	return translateBreakerError(bastion.Do(ctx, r.b, func(ctx context.Context) error {
		return r.next.DeleteSession(ctx, tokenHash)
	}), r.retryAfter)
}

func (r *breakerRepository) FindSessionsForUser(ctx context.Context, userID string) ([]Session, error) {
	sessions, err := bastion.Execute(ctx, r.b, func(ctx context.Context) ([]Session, error) {
		return r.next.FindSessionsForUser(ctx, userID)
	})
	return sessions, translateBreakerError(err, r.retryAfter)
}

func (r *breakerRepository) DeleteSessionsForUser(ctx context.Context, userID string) error {
	return translateBreakerError(bastion.Do(ctx, r.b, func(ctx context.Context) error {
		return r.next.DeleteSessionsForUser(ctx, userID)
	}), r.retryAfter)
}

func (r *breakerRepository) DeleteSessionsForUserExcept(ctx context.Context, userID, keepTokenHash string) error {
	return translateBreakerError(bastion.Do(ctx, r.b, func(ctx context.Context) error {
		return r.next.DeleteSessionsForUserExcept(ctx, userID, keepTokenHash)
	}), r.retryAfter)
}

func (r *breakerRepository) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	return translateBreakerError(bastion.Do(ctx, r.b, func(ctx context.Context) error {
		return r.next.DeleteExpiredSessions(ctx, now)
	}), r.retryAfter)
}

// translateBreakerError maps bastion's own rejection sentinels to this
// package's ErrUnavailable at the boundary — see
// internal/task/postgres_breaker.go's identical function for the full
// reasoning.
func translateBreakerError(err error, retryAfter time.Duration) error {
	if errors.Is(err, bastion.ErrOpenState) || errors.Is(err, bastion.ErrTooManyRequests) {
		return &unavailableError{cause: err, retryAfter: retryAfter}
	}
	return err
}

// unavailableError carries ErrUnavailable plus how long the caller
// should wait before retrying — see
// internal/task/postgres_breaker.go's identical type for the full
// reasoning.
type unavailableError struct {
	cause      error
	retryAfter time.Duration
}

func (e *unavailableError) Error() string {
	return fmt.Sprintf("%s: %s", ErrUnavailable, e.cause)
}

func (e *unavailableError) Is(target error) bool {
	return target == ErrUnavailable
}

func (e *unavailableError) RetryAfter() time.Duration {
	return e.retryAfter
}
