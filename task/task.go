package task

import "time"

// Status represents the current state of a task.
type Status string

const (
	StatusPending Status = "pending"
	StatusDone    Status = "done"
)

// Task is the core domain entity.
type Task struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      Status    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// Version is a Repository-managed optimistic-concurrency counter. It is
	// not part of the public wire format (json:"-"): callers never set it
	// directly. Service reads it via FindByID/FindAll and passes it back
	// unchanged to Update, which uses it to detect a concurrent write; see
	// Repository's doc comment.
	Version int `json:"-"`
}
