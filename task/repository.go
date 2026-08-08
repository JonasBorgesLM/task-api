package task

import "errors"

// ErrNotFound is returned when a task with the given ID does not exist.
var ErrNotFound = errors.New("task not found")

// Repository defines the contract for task persistence.
type Repository interface {
	Create(task Task) error
	FindByID(id string) (Task, error)
	FindAll() ([]Task, error)
	Update(task Task) error
	Delete(id string) error
}
