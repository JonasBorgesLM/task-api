package task

import "errors"

// ErrNotFound is returned when a task with the given ID does not exist.
var ErrNotFound = errors.New("task not found")

// ErrAlreadyExists is returned by Create when a task with the same ID already exists.
var ErrAlreadyExists = errors.New("task already exists")

// ErrInvalidInput is returned when the caller provides invalid data.
var ErrInvalidInput = errors.New("invalid input")

// ErrConflict is returned by Update when the task was modified by another
// writer between the caller's read and this write (optimistic concurrency
// failure). The caller should re-read the task and retry.
var ErrConflict = errors.New("task was modified concurrently")
