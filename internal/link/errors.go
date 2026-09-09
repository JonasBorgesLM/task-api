package link

import "errors"

// ErrNotOwner reports that the authenticated principal does not own the
// link a Revoke call addressed. It is never distinguished from "not
// found" in Handler's response — see handleServiceError's doc comment —
// matching this project's existing rule that a resource belonging to
// another user is reported identically to one that doesn't exist
// (.claude/rules/go-domain-errors.md rule 3).
//
// cairn's own sentinel errors (ErrCodeNotFound, ErrInvalidCode,
// ErrLinkExpired, ErrLinkRevoked, ErrDestinationRejected, ErrCodeExists,
// ErrVanityReserved, ErrVanityLength, ErrCodeSpaceExhausted,
// ErrStoreUnavailable) are this package's domain-error taxonomy too —
// this package does not wrap them in a second, redundant sentinel layer.
// cairn occupies the position of Service-layer domain logic here (see
// docs/INTEGRATION.md §4.1 in the cairn module: "cairn is Service-layer
// domain logic with its own Repository, not a repository itself"), so
// Handler.handleServiceError switches on cairn's errors directly, the
// same way it would switch on this package's own if cairn did not
// already define an equivalent set.
var ErrNotOwner = errors.New("link: principal does not own this link")
