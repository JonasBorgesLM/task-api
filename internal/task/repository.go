package task

import (
	"context"
)

// Repository defines the contract for task persistence. Every method that
// reads or writes an existing task takes the owning userID and scopes the
// operation to it: a task belonging to a different user is reported as
// ErrNotFound, never a distinct "forbidden" error — this deliberately
// avoids leaking whether a given task ID exists at all to a caller who
// doesn't own it. Ownership filtering is pushed into the query itself in
// postgresRepository (WHERE ... AND user_id = $N), the same reasoning as
// FindAll's pagination window below: fetch only what the caller could
// possibly be allowed to see, don't fetch-then-check in Go.
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
// statuses and priorities filter the result when non-empty; an empty (or
// nil) slice means "no filter on this field". Values *within* one slice
// combine with OR — a task matches if its status is any of the ones
// asked for — while the two slices combine with AND, so
// statuses=[pending,done] priorities=[high] means "pending or done, and
// high". Filtering happens before the limit/offset window is applied: a
// filtered page must window the filtered set, not the full table. This
// is pushed into the query itself in postgresRepository (a conditional
// IN clause), not fetch-then-filter in Go.
//
// Callers are expected to have validated and de-duplicated the values
// (Service.ListTasks does); a Repository does not re-check that a status
// names a real status.
//
// Update performs optimistic concurrency control keyed on Task.Version: the
// caller must pass back the Version it most recently read (via FindByID or
// FindAll), and Update returns ErrConflict — without applying the write —
// if the stored task's Version no longer matches, i.e. some other writer
// updated it in between. On success the stored Version is incremented, so
// the caller's next Update must use the Task returned by this call (or a
// fresh read) rather than reusing the pre-write value. Update takes no
// separate userID parameter — task.UserID (set from a prior ownership-checked
// FindByID) is what's checked against the stored row; a mismatch is
// ErrNotFound, same as FindByID/Delete.
type Repository interface {
	Create(ctx context.Context, task Task) error
	FindByID(ctx context.Context, id, userID string) (Task, error)
	FindAll(ctx context.Context, userID string, limit, offset int, statuses []Status, priorities []Priority) ([]Task, error)
	Update(ctx context.Context, task Task) error
	Delete(ctx context.Context, id, userID string) error
}
