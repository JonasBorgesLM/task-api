package task

import (
	"context"
)

// Repository defines the contract for task persistence.
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
	FindAll(ctx context.Context) ([]Task, error)
	Update(ctx context.Context, task Task) error
	Delete(ctx context.Context, id string) error
}
