package attachment

import "errors"

// ErrNotFound is returned when no attachment matches the given
// identifier *for the acting user*. It deliberately does not distinguish
// "no such attachment" from "an attachment that belongs to someone else",
// mirroring internal/task: reporting those differently would let a
// non-owner confirm that a given ID exists.
var ErrNotFound = errors.New("attachment not found")

// ErrTaskNotFound is returned by Create when the task an attachment is
// being hung off does not exist for the acting user. Distinct from
// ErrNotFound because the two describe different resources and a handler
// maps them to different messages — but for the same reason as above,
// this one also covers "the task exists and belongs to someone else".
var ErrTaskNotFound = errors.New("task not found")

// ErrAlreadyExists is returned by Create when an attachment with the same
// ID, or the same storage key, is already stored. Both indicate a
// collision in server-generated identifiers rather than anything the
// caller did, so a caller seeing this should treat it as a retryable
// server-side fault, not as invalid input.
var ErrAlreadyExists = errors.New("attachment already exists")

// ErrInvalidInput is returned when the caller provides invalid data.
var ErrInvalidInput = errors.New("invalid input")

// ErrDependencyUnavailable is returned when a Repository call fails because
// the underlying store itself is the problem — a refused or dropped
// connection, the database shedding load or shutting down, a pool with no
// connection to spare — rather than because of anything about the request
// itself. Handler maps it to 503, not 500: the request was reasonable and
// worth retrying, once the dependency recovers. See
// docs/DECISIONS.md § "Classificação positiva de erro de infraestrutura".
var ErrDependencyUnavailable = errors.New("dependency unavailable")
