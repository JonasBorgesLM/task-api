package user

import "errors"

// ErrNotFound is returned when a user or session does not exist.
var ErrNotFound = errors.New("user not found")

// ErrAlreadyExists is returned by Register when the given email is already
// registered.
var ErrAlreadyExists = errors.New("user already exists")

// ErrInvalidInput is returned when the caller provides invalid data (a
// malformed email, a too-short password, ...).
var ErrInvalidInput = errors.New("invalid input")

// ErrInvalidCredentials is returned by Authenticate for both an unknown
// email and a correct email with the wrong password — deliberately the
// same error either way, so a caller can never distinguish "no such
// account" from "wrong password" and enumerate registered emails.
var ErrInvalidCredentials = errors.New("invalid email or password")

// ErrDependencyUnavailable is returned when a Repository call fails because
// the underlying store itself is the problem — a refused or dropped
// connection, the database shedding load or shutting down, a pool with no
// connection to spare — rather than because of anything about the request
// itself. Handler maps it to 503, not 500: the request was reasonable and
// worth retrying, once the dependency recovers. See
// docs/DECISIONS.md § "Classificação positiva de erro de infraestrutura".
var ErrDependencyUnavailable = errors.New("dependency unavailable")
