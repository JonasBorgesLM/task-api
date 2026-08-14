package task

import (
	"context"
	"sort"
	"sync"
)

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

// Create persists a new task. Returns ErrAlreadyExists if the ID is already
// taken. The stored Version always starts at 1, regardless of what the
// caller set on task.Version.
func (r *memoryRepository) Create(ctx context.Context, task Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.store[task.ID]; exists {
		return ErrAlreadyExists
	}

	task.Version = 1
	r.store[task.ID] = task
	return nil
}

// FindByID returns the task with the given ID, scoped to userID — see
// Repository's doc comment on ownership. Returns ErrNotFound if absent or
// owned by a different user.
func (r *memoryRepository) FindByID(ctx context.Context, id, userID string) (Task, error) {
	if err := ctx.Err(); err != nil {
		return Task{}, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	task, ok := r.store[id]
	if !ok || task.UserID != userID {
		return Task{}, ErrNotFound
	}

	return task, nil
}

// FindAll returns a snapshot of userID's tasks ordered by CreatedAt
// ascending (ties broken by ID) — the store is a Go map, so this ordering
// is applied here rather than relied upon from iteration — windowed to at
// most limit results starting at offset, per Repository's contract.
func (r *memoryRepository) FindAll(ctx context.Context, userID string, limit, offset int) ([]Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	tasks := make([]Task, 0, len(r.store))
	for _, task := range r.store {
		if task.UserID != userID {
			continue
		}
		tasks = append(tasks, task)
	}

	sort.Slice(tasks, func(i, j int) bool {
		if !tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
			return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
		}
		return tasks[i].ID < tasks[j].ID
	})

	return paginateTasks(tasks, limit, offset), nil
}

// paginateTasks returns the sub-slice of tasks starting at offset and
// containing at most limit elements. limit < 0 means "no limit" (return
// everything from offset onward). offset or limit values beyond the end of
// tasks yield an empty result rather than an error — matching
// Repository.FindAll's documented contract.
func paginateTasks(tasks []Task, limit, offset int) []Task {
	if offset >= len(tasks) {
		return []Task{}
	}
	tasks = tasks[offset:]

	if limit >= 0 && limit < len(tasks) {
		tasks = tasks[:limit]
	}

	return tasks
}

// Update replaces an existing task. Returns ErrNotFound if the ID does not
// exist or belongs to a different user than task.UserID (see Repository's
// doc comment), and ErrConflict if task.Version does not match the stored
// Version — i.e. the task was modified by another writer since task was
// read. On success the stored Version is incremented.
func (r *memoryRepository) Update(ctx context.Context, task Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	stored, ok := r.store[task.ID]
	if !ok || stored.UserID != task.UserID {
		return ErrNotFound
	}
	if task.Version != stored.Version {
		return ErrConflict
	}

	task.Version = stored.Version + 1
	r.store[task.ID] = task
	return nil
}

// Delete removes the task with the given ID, scoped to userID. Returns
// ErrNotFound if absent or owned by a different user.
func (r *memoryRepository) Delete(ctx context.Context, id, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	stored, ok := r.store[id]
	if !ok || stored.UserID != userID {
		return ErrNotFound
	}

	delete(r.store, id)
	return nil
}
