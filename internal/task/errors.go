package task

import "errors"

// ErrNotFound is returned when a task with the given ID does not exist.
var ErrNotFound = errors.New("task not found")

// ErrAlreadyExists is returned by Create when a task with the same ID already exists.
var ErrAlreadyExists = errors.New("task already exists")

// ErrInvalidInput is returned when the caller provides invalid data.
var ErrInvalidInput = errors.New("invalid input")

// ErrConflict is returned by Update when the task was modified by another
// writer between the caller's read and this write (optimistic concurrency
// failure). The caller should re-read the task and retry.
var ErrConflict = errors.New("task was modified concurrently")

// ErrInvalidTransition is returned by Service.TransitionStatus when the
// requested target status is not reachable from the task's current status
// (see Service's legalTransitions table). Distinct from ErrConflict: this
// is not a race with another writer, it's a request that conflicts with
// the resource's current state regardless of timing — both map to 409,
// but with different messages.
var ErrInvalidTransition = errors.New("invalid status transition")

// ErrDependencyUnavailable is returned when a Repository call fails because
// the underlying store itself is the problem — a refused or dropped
// connection, the database shedding load or shutting down, a pool with no
// connection to spare — rather than because of anything about the request
// itself. Handler maps it to 503, not 500: the request was reasonable and
// worth retrying, once the dependency recovers.
//
// This is deliberately a package-level sentinel like every other error
// here, not a PostgreSQL type — Service and Handler check it with
// errors.Is exactly like ErrNotFound or ErrConflict, and stay unaware that
// PostgreSQL exists. See docs/DECISIONS.md § "Classificação positiva de
// erro de infraestrutura".
var ErrDependencyUnavailable = errors.New("dependency unavailable")

// ErrUnavailable is returned by a Repository wrapped with a circuit
// breaker (see postgres_breaker.go) when the breaker itself refuses a
// call — the circuit is open, or half-open has already admitted its
// allowance of probes. The wrapped operation was never invoked in
// either case.
//
// Distinct from ErrDependencyUnavailable: that sentinel means "the call
// was attempted and the database told us it is unhealthy, once". This
// one means "the breaker, from a run of prior evidence, already knows
// better than to try" — the recovery moment is knowable in advance (the
// breaker's own openTimeout), which is why Handler answers it with a
// 503 carrying Retry-After. See docs/DECISIONS.md § "Breaker
// compartilhado no PostgreSQL".
var ErrUnavailable = errors.New("database unavailable")
