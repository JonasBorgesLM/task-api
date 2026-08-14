package main

import "testing"

// TestRun_NegativeUsers_ReturnsError verifies that -users is validated
// before run() ever touches config or a database connection.
func TestRun_NegativeUsers_ReturnsError(t *testing.T) {
	err := run(-1, "password123", 10, false)
	if err == nil {
		t.Fatal("run() with -users=-1: expected an error, got nil")
	}
}

// TestRun_NegativeTasksPerUser_ReturnsError mirrors
// TestRun_NegativeUsers_ReturnsError for -tasks-per-user.
func TestRun_NegativeTasksPerUser_ReturnsError(t *testing.T) {
	err := run(5, "password123", -1, false)
	if err == nil {
		t.Fatal("run() with -tasks-per-user=-1: expected an error, got nil")
	}
}
