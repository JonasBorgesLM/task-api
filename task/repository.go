package task

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a task with the given ID does not exist.
var ErrNotFound = errors.New("task not found")

// ErrAlreadyExists is returned by Create when a task with the same ID already exists.
var ErrAlreadyExists = errors.New("task already exists")

// Repository defines the contract for task persistence.
type Repository interface {
	Create(ctx context.Context, task Task) error
	FindByID(ctx context.Context, id string) (Task, error)
	FindAll(ctx context.Context) ([]Task, error)
	Update(ctx context.Context, task Task) error
	Delete(ctx context.Context, id string) error
}
