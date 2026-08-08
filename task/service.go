package task

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalidInput is returned when the caller provides invalid data.
var ErrInvalidInput = errors.New("invalid input")

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
func (s *Service) CreateTask(title, description string) (Task, error) {
	if strings.TrimSpace(title) == "" {
		return Task{}, fmt.Errorf("%w: title must not be empty", ErrInvalidInput)
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

	if err := s.repo.Create(task); err != nil {
		return Task{}, err
	}

	return task, nil
}

// GetTask retrieves a Task by its ID.
func (s *Service) GetTask(id string) (Task, error) {
	return s.repo.FindByID(id)
}

// ListTasks returns all stored tasks.
func (s *Service) ListTasks() ([]Task, error) {
	return s.repo.FindAll()
}

// UpdateTask updates the title and description of an existing Task.
// CreatedAt is never modified. UpdatedAt is set to now.
func (s *Service) UpdateTask(id, title, description string) (Task, error) {
	if strings.TrimSpace(title) == "" {
		return Task{}, fmt.Errorf("%w: title must not be empty", ErrInvalidInput)
	}

	task, err := s.repo.FindByID(id)
	if err != nil {
		return Task{}, err
	}

	task.Title = title
	task.Description = description
	task.UpdatedAt = time.Now()

	if err := s.repo.Update(task); err != nil {
		return Task{}, err
	}

	return task, nil
}

// DeleteTask removes a Task by its ID.
func (s *Service) DeleteTask(id string) error {
	return s.repo.Delete(id)
}

// CompleteTask marks a Task as done. It is idempotent: if the Task is
// already Done, it returns the Task unchanged without updating UpdatedAt.
func (s *Service) CompleteTask(id string) (Task, error) {
	task, err := s.repo.FindByID(id)
	if err != nil {
		return Task{}, err
	}

	if task.Status == StatusDone {
		return task, nil
	}

	task.Status = StatusDone
	task.UpdatedAt = time.Now()

	if err := s.repo.Update(task); err != nil {
		return Task{}, err
	}

	return task, nil
}
