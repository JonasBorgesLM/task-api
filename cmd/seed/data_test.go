package main

import "testing"

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
