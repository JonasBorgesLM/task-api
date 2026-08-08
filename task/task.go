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
}
