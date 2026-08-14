package main

import (
	"strings"
	"testing"
)

// TestRun_InvalidDirection verifies that an unrecognized -direction value
// is rejected before run() ever touches config or a database connection —
// direction validation is the first thing run() does, so this needs no
// DATABASE_URL and no PostgreSQL instance.
func TestRun_InvalidDirection(t *testing.T) {
	err := run("sideways")
	if err == nil {
		t.Fatal("run() with an invalid -direction: expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "sideways") {
		t.Errorf("run() error = %q, want it to mention the invalid value", err.Error())
	}
}

// TestRun_MissingDatabaseURL verifies that a valid direction with no
// DATABASE_URL configured fails with a clear error instead of a confusing
// downstream failure from sql.Open on an empty DSN.
func TestRun_MissingDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DOTENV_PATH", "/nonexistent") // don't let a real .env in the working directory supply one

	err := run("up")
	if err == nil {
		t.Fatal("run() with no DATABASE_URL: expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("run() error = %q, want it to mention DATABASE_URL", err.Error())
	}
}
