package pgerr

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// Real SQLSTATEs, not invented ones — table-driven so the allowlist reads
// as a table, the shape docs/DECISIONS.md's classification section
// describes it as.
//
// 22P02 (invalid_text_representation — a malformed UUID, the concrete case
// that motivated this package) is the explicit negative case issue #278
// asked for: a client sending bad input must classify as false, the same
// as any other input the database was never going to be confused about.
func TestIsInfrastructureFailure(t *testing.T) {
	tests := []struct {
		name string
		code string // pgconn.PgError.Code (SQLSTATE)
		want bool
	}{
		{"connection_exception", "08000", true},
		{"connection_does_not_exist", "08003", true},
		{"connection_failure", "08006", true},
		{"sqlclient_unable_to_establish_sqlconnection", "08001", true},
		{"insufficient_resources", "53000", true},
		{"disk_full", "53100", true},
		{"too_many_connections", "53300", true},
		{"operator_intervention", "57000", true},
		{"admin_shutdown", "57P01", true},
		{"crash_shutdown", "57P02", true},

		{"invalid_text_representation_malformed_uuid", "22P02", false},
		{"unique_violation", "23505", false},
		{"foreign_key_violation", "23503", false},
		{"not_null_violation", "23502", false},
		{"undefined_table", "42P01", false},
		{"syntax_error", "42601", false},
		{"numeric_value_out_of_range", "22003", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &pgconn.PgError{Code: tt.code}
			if got := IsInfrastructureFailure(err); got != tt.want {
				t.Errorf("IsInfrastructureFailure(SQLSTATE %s) = %v, want %v", tt.code, got, tt.want)
			}

			// Must still classify correctly wrapped, the same way every
			// real call site wraps it (fmt.Errorf("postgres: ...: %w", err)).
			wrapped := fmt.Errorf("postgres: some operation: %w", err)
			if got := IsInfrastructureFailure(wrapped); got != tt.want {
				t.Errorf("IsInfrastructureFailure(wrapped SQLSTATE %s) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}

func TestIsInfrastructureFailure_DriverAndDeadlineErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"bad connection", driver.ErrBadConn, true},
		{"wrapped bad connection", fmt.Errorf("postgres: query: %w", driver.ErrBadConn), true},
		{"deadline exceeded (pool wait or DBCallTimeout)", context.DeadlineExceeded, true},
		{"wrapped deadline exceeded", fmt.Errorf("postgres: query: %w", context.DeadlineExceeded), true},

		{"ordinary error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsInfrastructureFailure(tt.err); got != tt.want {
				t.Errorf("IsInfrastructureFailure(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// A *pgconn.PgError whose SQLSTATE class was never enumerated must return
// false, not panic and not default to true. This is the allowlist
// direction itself under test: an unrecognised class is not "infrastructure
// until proven otherwise", it is "not infrastructure until shown to be".
func TestIsInfrastructureFailure_UnknownClassIsFalse(t *testing.T) {
	err := &pgconn.PgError{Code: "99999"}
	if IsInfrastructureFailure(err) {
		t.Fatal("IsInfrastructureFailure(unrecognised SQLSTATE class) = true, want false")
	}
}
