// Package pgerr classifies a PostgreSQL error as an infrastructure failure
// or not, so a repository can tell "the database itself is the problem"
// apart from "the database gave a well-formed answer" without every
// repository writing its own copy of the same classification.
//
// It is a platform package deliberately kept separate from the domain
// repositories (internal/task, internal/user, internal/attachment): all
// three import it, and it imports none of them, matching
// internal/platform/migrate's own shape for cross-domain PostgreSQL
// plumbing.
package pgerr

import (
	"context"
	"database/sql/driver"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// IsInfrastructureFailure reports whether err represents PostgreSQL itself
// being the problem — a refused or dropped connection, the server
// shedding load, an administrator shutting it down, a pooled connection
// that has gone bad, or the caller's own deadline expiring while waiting
// for one of those — as opposed to a well-formed answer from a healthy
// database: a constraint violation, a malformed value, no matching row.
//
// # Written as an allowlist, on purpose
//
// An error this function has never seen returns false — the same
// behaviour every caller already had before this function existed — never
// true. See docs/DECISIONS.md § "Classificação positiva de erro de
// infraestrutura" for why that direction, not the other one, is the safe
// one to fail in: an allowlist that misses a case under-classifies, which
// is today's status quo; an exclusion list that misses one would
// over-classify a client's own bad input as an outage.
//
// SQLSTATE class 22 ("data exception" — a malformed UUID, an out-of-range
// value) is deliberately not in this allowlist: it is the caller's input
// arriving through the same return path as a real fault, and the entire
// reason this function exists is to keep the two apart.
func IsInfrastructureFailure(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code[:2] {
		case "08", // connection exception
			"53", // insufficient resources
			"57": // operator intervention
			return true
		default:
			return false
		}
	}

	// driver.ErrBadConn: database/sql discovered a pooled connection is
	// dead and could not silently retry on a fresh one. context.DeadlineExceeded:
	// most often DBCallTimeout (or the caller's own deadline) expiring
	// while a query waited for a free pooled connection — evidence of
	// the pool under pressure, not of anything the caller sent.
	return errors.Is(err, driver.ErrBadConn) || errors.Is(err, context.DeadlineExceeded)
}
