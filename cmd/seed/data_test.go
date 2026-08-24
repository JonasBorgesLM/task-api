package main

import (
	"testing"

	"github.com/JonasBorgesLM/task-api/internal/task"
)

// TestRandomTask_NeverEmptyTitle verifies that randomTask always produces a
// non-empty title — task.Service.CreateTask rejects an empty title, so a
// bug here would make every seed run fail on the very first task.
func TestRandomTask_NeverEmptyTitle(t *testing.T) {
	for range 200 {
		title, _ := randomTask()
		if title == "" {
			t.Fatal("randomTask() returned an empty title")
		}
	}
}

// TestRandomTask_WithinLengthLimits verifies that generated title and
// description stay comfortably under task.Service's length limits
// (200/2000 characters) — the fixed word lists in data.go guarantee this
// by construction, but a future edit that appends abnormally long entries
// should fail this test rather than fail every seed run instead.
func TestRandomTask_WithinLengthLimits(t *testing.T) {
	const maxTitleLen = 200
	const maxDescriptionLen = 2000

	for range 200 {
		title, description := randomTask()
		if len(title) > maxTitleLen {
			t.Fatalf("randomTask() title %q is %d characters, want <= %d", title, len(title), maxTitleLen)
		}
		if len(description) > maxDescriptionLen {
			t.Fatalf("randomTask() description %q is %d characters, want <= %d", description, len(description), maxDescriptionLen)
		}
	}
}

// TestRandomTask_ProducesVariety verifies that repeated calls don't
// degenerate into always returning the same pair — a sanity check that the
// word lists are actually being indexed randomly, not just at index 0.
func TestRandomTask_ProducesVariety(t *testing.T) {
	seen := make(map[string]bool)
	for range 50 {
		title, _ := randomTask()
		seen[title] = true
	}
	if len(seen) < 2 {
		t.Fatalf("randomTask() produced only %d distinct title(s) across 50 calls, want variety", len(seen))
	}
}

// TestRandomStatus_ProducesEveryValue verifies that, across enough calls,
// randomStatus() eventually returns every task.Status statusWeights
// assigns a nonzero weight to — a bug that zeroed out or misspelled one
// entry would otherwise only show up as a subtly skewed seed dataset,
// never a hard failure.
func TestRandomStatus_ProducesEveryValue(t *testing.T) {
	seen := make(map[task.Status]bool)
	for range 2000 {
		seen[randomStatus()] = true
	}
	for _, want := range []task.Status{task.StatusPending, task.StatusInProgress, task.StatusDone, task.StatusCancelled} {
		if !seen[want] {
			t.Errorf("randomStatus() never produced %q across 2000 calls", want)
		}
	}
}

// TestRandomStatus_NeverProducesUnknownValue guards statusWeights against
// a typo'd task.Status literal — randomStatus() must only ever return one
// of the four values Service actually understands.
func TestRandomStatus_NeverProducesUnknownValue(t *testing.T) {
	valid := map[task.Status]bool{
		task.StatusPending: true, task.StatusInProgress: true,
		task.StatusDone: true, task.StatusCancelled: true,
	}
	for range 500 {
		if got := randomStatus(); !valid[got] {
			t.Fatalf("randomStatus() returned %q, which is not a known task.Status", got)
		}
	}
}

// TestRandomPriority_ProducesEveryValue mirrors
// TestRandomStatus_ProducesEveryValue for randomPriority().
func TestRandomPriority_ProducesEveryValue(t *testing.T) {
	seen := make(map[task.Priority]bool)
	for range 500 {
		seen[randomPriority()] = true
	}
	for _, want := range []task.Priority{task.PriorityLow, task.PriorityMedium, task.PriorityHigh} {
		if !seen[want] {
			t.Errorf("randomPriority() never produced %q across 500 calls", want)
		}
	}
}
