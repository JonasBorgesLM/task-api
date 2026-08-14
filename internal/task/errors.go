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

// ErrInvalidTransition is returned by Service.TransitionStatus when the
// requested target status is not reachable from the task's current status
// (see Service's legalTransitions table). Distinct from ErrConflict: this
// is not a race with another writer, it's a request that conflicts with
// the resource's current state regardless of timing — both map to 409,
// but with different messages.
var ErrInvalidTransition = errors.New("invalid status transition")
