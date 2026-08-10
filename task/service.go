package task

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Maximum accepted lengths for user-supplied text fields. These bound
// storage/response size and catch obviously malformed input; they are
// deliberately generous for a task title/description.
const (
	maxTitleLen       = 200
	maxDescriptionLen = 2000
)

// Service holds the business logic for task management.
type Service struct {
	repo Repository
}

// NewService returns a new Service with the given Repository.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// newID generates a random UUID v4.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("failed to generate task ID: %w", err)
	}

	// Set version 4 (bits 4–7 of byte 6).
	b[6] = b[6]&0x0f | 0x40
	// Set variant bits (bits 6–7 of byte 8 to 0b10).
	b[8] = b[8]&0x3f | 0x80

	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:],
	), nil
}

// CreateTask validates input, builds a new Task and persists it.
func (s *Service) CreateTask(ctx context.Context, title, description string) (Task, error) {
	title, description, err := validateTitleAndDescription(title, description)
	if err != nil {
		return Task{}, err
	}

	id, err := newID()
	if err != nil {
		return Task{}, fmt.Errorf("create task: %w", err)
	}

	now := time.Now()
	task := Task{
		ID:          id,
		Title:       title,
		Description: description,
		Status:      StatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.repo.Create(ctx, task); err != nil {
		return Task{}, fmt.Errorf("create task: %w", err)
	}

	return task, nil
}

// GetTask retrieves a Task by its ID.
func (s *Service) GetTask(ctx context.Context, id string) (Task, error) {
	task, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Task{}, fmt.Errorf("get task: %w", err)
	}
	return task, nil
}

// ListTasks returns all stored tasks, ordered deterministically by
// CreatedAt (oldest first) and then by ID to break ties. The Repository
// itself makes no ordering guarantee (memoryRepository iterates a map), so
// this ordering is applied here rather than relied upon from storage.
func (s *Service) ListTasks(ctx context.Context) ([]Task, error) {
	tasks, err := s.repo.FindAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	sort.Slice(tasks, func(i, j int) bool {
		if !tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
			return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
		}
		return tasks[i].ID < tasks[j].ID
	})

	return tasks, nil
}

// UpdateTask updates the title and description of an existing Task.
// CreatedAt is never modified. UpdatedAt is set to now.
//
// UpdateTask performs a read-modify-write against the Repository with
// optimistic concurrency control: it passes back the Version it read from
// FindByID, and Repository.Update rejects the write with ErrConflict if
// another writer updated the same task in between. The caller should treat
// ErrConflict as retryable — re-read the task and try again.
func (s *Service) UpdateTask(ctx context.Context, id, title, description string) (Task, error) {
	title, description, err := validateTitleAndDescription(title, description)
	if err != nil {
		return Task{}, err
	}

	task, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Task{}, fmt.Errorf("update task: %w", err)
	}

	task.Title = title
	task.Description = description
	task.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, task); err != nil {
		return Task{}, fmt.Errorf("update task: %w", err)
	}

	return task, nil
}

// DeleteTask removes a Task by its ID.
func (s *Service) DeleteTask(ctx context.Context, id string) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	return nil
}

// CompleteTask marks a Task as done. It is idempotent: if the Task is
// already Done, it returns the Task unchanged without updating UpdatedAt
// (and without calling Repository.Update at all, so it cannot ErrConflict
// in that case).
//
// Like UpdateTask, this uses optimistic concurrency control via the
// Version read from FindByID; see UpdateTask's doc comment.
func (s *Service) CompleteTask(ctx context.Context, id string) (Task, error) {
	task, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Task{}, fmt.Errorf("complete task: %w", err)
	}

	if task.Status == StatusDone {
		return task, nil
	}

	task.Status = StatusDone
	task.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, task); err != nil {
		return Task{}, fmt.Errorf("complete task: %w", err)
	}

	return task, nil
}

// validateTitleAndDescription trims title and description and validates
// their length: title must be non-empty (after trimming) and at most
// maxTitleLen characters; description must be at most maxDescriptionLen
// characters. It returns the trimmed values, or ErrInvalidInput describing
// which constraint failed.
func validateTitleAndDescription(title, description string) (string, string, error) {
	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)

	if title == "" {
		return "", "", fmt.Errorf("%w: title must not be empty", ErrInvalidInput)
	}
	if len(title) > maxTitleLen {
		return "", "", fmt.Errorf("%w: title must be at most %d characters", ErrInvalidInput, maxTitleLen)
	}
	if len(description) > maxDescriptionLen {
		return "", "", fmt.Errorf("%w: description must be at most %d characters", ErrInvalidInput, maxDescriptionLen)
	}

	return title, description, nil
}
