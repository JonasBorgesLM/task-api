package task

import (
	"context"
)

// Repository defines the contract for task persistence.
//
// FindAll returns tasks ordered deterministically by CreatedAt ascending,
// ties broken by ID, starting at offset and containing at most limit
// results — limit < 0 means "no limit" (every task from offset onward).
// This ordering and windowing is Repository's responsibility, not
// Service's: it lets a backing store apply LIMIT/OFFSET (or an index-backed
// ORDER BY) in the query itself instead of fetching every row just to
// discard most of them. offset or limit values beyond the end of the data
// are not an error — they yield an empty result.
//
// Update performs optimistic concurrency control keyed on Task.Version: the
// caller must pass back the Version it most recently read (via FindByID or
// FindAll), and Update returns ErrConflict — without applying the write —
// if the stored task's Version no longer matches, i.e. some other writer
// updated it in between. On success the stored Version is incremented, so
// the caller's next Update must use the Task returned by this call (or a
// fresh read) rather than reusing the pre-write value.
type Repository interface {
	Create(ctx context.Context, task Task) error
	FindByID(ctx context.Context, id string) (Task, error)
	FindAll(ctx context.Context, limit, offset int) ([]Task, error)
	Update(ctx context.Context, task Task) error
	Delete(ctx context.Context, id string) error
}

// Pinger is implemented by Repository implementations that can verify
// their backing store is actually reachable, as opposed to merely
// constructed. It is deliberately not part of the Repository interface
// itself: memoryRepository has nothing external to check (it is always
// reachable by construction), and Service/Handler never need this
// capability to serve a normal request — only a readiness check does (see
// cmd/api/health.go's registerReadinessRoute), via a type assertion
// against the concrete Repository in use.
type Pinger interface {
	Ping(ctx context.Context) error
}
