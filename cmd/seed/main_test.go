package main

import (
	"strings"
	"testing"
)

// TestRun_NegativeUsers_ReturnsError verifies that -users is validated
// before run() ever touches config or a database connection.
func TestRun_NegativeUsers_ReturnsError(t *testing.T) {
	err := run(seedOptions{users: -1, password: "password123", tasksPerUser: 10})
	if err == nil {
		t.Fatal("run() with -users=-1: expected an error, got nil")
	}
}

// TestRun_NegativeTasksPerUser_ReturnsError mirrors
// TestRun_NegativeUsers_ReturnsError for -tasks-per-user.
func TestRun_NegativeTasksPerUser_ReturnsError(t *testing.T) {
	err := run(seedOptions{users: 5, password: "password123", tasksPerUser: -1})
	if err == nil {
		t.Fatal("run() with -tasks-per-user=-1: expected an error, got nil")
	}
}

// --- -reset safety guard ---

// TestRun_ResetAgainstRemoteDatabase_IsRefused verifies the guard fires
// before any connection is opened: -reset TRUNCATEs users, sessions and
// tasks irreversibly, so pointing it at a non-local DATABASE_URL by
// accident (a copy-pasted production string) must fail rather than
// succeed quietly.
func TestRun_ResetAgainstRemoteDatabase_IsRefused(t *testing.T) {
	t.Setenv("DOTENV_PATH", t.TempDir()+"/absent.env") // ignore any local .env
	t.Setenv("DATABASE_URL", "postgres://user:pass@db.production.example.com:5432/app")

	err := run(seedOptions{users: 0, reset: true})
	if err == nil {
		t.Fatal("run() with -reset against a remote DATABASE_URL: expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "-allow-remote-reset") {
		t.Errorf("error = %q, want it to name the flag that overrides the guard", err)
	}
}

// TestRun_ResetAgainstRemoteDatabase_AllowedWithFlag verifies the escape
// hatch exists: with -allow-remote-reset the guard no longer refuses, and
// run() proceeds far enough to attempt a real connection (which fails here
// only because the host doesn't resolve — a different error entirely).
func TestRun_ResetAgainstRemoteDatabase_AllowedWithFlag(t *testing.T) {
	t.Setenv("DOTENV_PATH", t.TempDir()+"/absent.env")
	t.Setenv("DATABASE_URL", "postgres://user:pass@db.invalid.example:5432/app")

	err := run(seedOptions{users: 0, reset: true, allowRemoteReset: true})
	if err == nil {
		t.Fatal("expected a connection error, got nil")
	}
	if strings.Contains(err.Error(), "-allow-remote-reset") {
		t.Errorf("guard still refused despite -allow-remote-reset: %v", err)
	}
}

// TestIsLocalDatabase covers the classification the guard depends on. The
// helper is deliberately conservative: anything it cannot positively
// identify as local (notably libpq's key/value DSN form, whose target it
// would have to reimplement a parser to read) is treated as remote, since
// the cost of being wrong in that direction is one extra flag rather than
// an unrecoverable TRUNCATE.
func TestIsLocalDatabase(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"localhost", "postgres://task_api:task_api@localhost:5432/task_api?sslmode=disable", true},
		{"127.0.0.1", "postgres://task_api:task_api@127.0.0.1:5432/task_api", true},
		{"IPv6 loopback", "postgres://task_api:task_api@[::1]:5432/task_api", true},
		{"postgresql scheme", "postgresql://task_api@localhost/task_api", true},

		{"remote host", "postgres://user:pass@db.production.example.com:5432/app", false},
		{"docker compose service name", "postgres://task_api:task_api@postgres:5432/task_api", false},
		{"private IP", "postgres://user:pass@10.0.1.15:5432/app", false},
		{"key/value DSN (unparseable target)", "host=localhost user=task_api dbname=task_api", false},
		{"unix socket (no host)", "postgres:///task_api", false},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLocalDatabase(tc.url); got != tc.want {
				t.Errorf("isLocalDatabase(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}
