package task

import (
	"errors"
	"sync"
)

// errAlreadyExists is returned by Create when a task with the same ID already exists.
var errAlreadyExists = errors.New("task already exists")

// memoryRepository is an in-memory implementation of Repository.
type memoryRepository struct {
	mu    sync.RWMutex
	store map[string]Task
}

// NewMemoryRepository returns a new in-memory Repository.
func NewMemoryRepository() Repository {
	return &memoryRepository{
		store: make(map[string]Task),
	}
}

// Create persists a new task. Returns errAlreadyExists if the ID is already taken.
func (r *memoryRepository) Create(task Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.store[task.ID]; exists {
		return errAlreadyExists
	}

	r.store[task.ID] = task
	return nil
}

// FindByID returns the task with the given ID. Returns ErrNotFound if absent.
func (r *memoryRepository) FindByID(id string) (Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	task, ok := r.store[id]
	if !ok {
		return Task{}, ErrNotFound
	}

	return task, nil
}

// FindAll returns a snapshot of all tasks as a new slice.
func (r *memoryRepository) FindAll() ([]Task, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tasks := make([]Task, 0, len(r.store))
	for _, task := range r.store {
		tasks = append(tasks, task)
	}

	return tasks, nil
}

// Update replaces an existing task. Returns ErrNotFound if the ID does not exist.
func (r *memoryRepository) Update(task Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.store[task.ID]; !ok {
		return ErrNotFound
	}

	r.store[task.ID] = task
	return nil
}

// Delete removes the task with the given ID. Returns ErrNotFound if absent.
func (r *memoryRepository) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.store[id]; !ok {
		return ErrNotFound
	}

	delete(r.store, id)
	return nil
}
