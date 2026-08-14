package task

import "time"

// Status represents the current state of a task in its workflow. Which
// transitions between these values are legal is decided entirely by
// Service (see its legalTransitions table) — the database CHECK
// constraint on this column only fences the set of valid values, exactly
// as documented on 0001_create_tasks_table.up.sql /
// 0005_expand_task_status_and_priority.up.sql.
type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
	StatusCancelled  Status = "cancelled"
)

// Priority represents how urgently a task should be worked on. Unlike
// Status, Priority has no transition rules — a caller may set it to any
// valid value at any time via CreateTask/UpdateTask.
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityMedium Priority = "medium"
	PriorityHigh   Priority = "high"
)

// Task is the core domain entity.
type Task struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      Status    `json:"status"`
	Priority    Priority  `json:"priority"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// UserID identifies the owning user. It is not part of the public wire
	// format (json:"-") — every task returned by the API already belongs
	// to the caller by construction (see Repository's ownership-scoped
	// methods), so echoing it back would be redundant, not additive,
	// information.
	UserID string `json:"-"`

	// Version is a Repository-managed optimistic-concurrency counter. It is
	// not part of the public wire format (json:"-"): callers never set it
	// directly. Service reads it via FindByID/FindAll and passes it back
	// unchanged to Update, which uses it to detect a concurrent write; see
	// Repository's doc comment.
	Version int `json:"-"`
}
