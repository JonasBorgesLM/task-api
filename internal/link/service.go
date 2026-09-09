// Package link is the short-link subsystem — task-api's first consumer
// of github.com/JonasBorgesLM/cairn (issues #209-#217, 15.A1-15.A9).
//
// Layering matches every other domain package (Handler → Service →
// cairn), but cairn itself is the "Repository" position's inhabitant is
// not: it is Service-layer domain logic with its own storage, per
// cairn's own docs/INTEGRATION.md §4.1. Service's whole job is the one
// thing cairn deliberately does not do — an ownership check on Revoke,
// see ErrNotOwner's doc comment and cairn's own ADR-0010.
package link

import (
	"context"
	"fmt"
	"time"

	"github.com/JonasBorgesLM/cairn"
)

// Service wraps a *cairn.Shortener and the cairn.OwnerLister capability
// its Store must implement (required for List — see NewService).
type Service struct {
	shortener *cairn.Shortener
	lister    cairn.OwnerLister
}

// NewService returns a Service backed by shortener. store must implement
// cairn.OwnerLister — the same Store shortener itself was constructed
// with — or NewService fails immediately: a missing capability is a
// startup error, never a runtime surprise on someone's first call to
// List (see cairn's store.go, OwnerLister's own doc comment).
func NewService(shortener *cairn.Shortener, store cairn.Store) (*Service, error) {
	lister, ok := store.(cairn.OwnerLister)
	if !ok {
		return nil, fmt.Errorf("link: store %T does not implement cairn.OwnerLister, required for GET /v1/links", store)
	}
	return &Service{shortener: shortener, lister: lister}, nil
}

// CreateRequest is one Create call's caller-supplied input. Its own type
// rather than exposing cairn.CreateOption on Service's signature keeps
// cairn-specific option construction inside this package — Handler
// builds a CreateRequest from the decoded JSON body, never a
// cairn.CreateOption directly (go-layering.md: Handler never touches
// Shortener, or its option types, directly).
type CreateRequest struct {
	// RawURL is the destination to shorten, exactly as the caller sent
	// it — cairn.Create parses and validates it (SR-05, SR-06, SR-08,
	// SR-10) and evaluates it against Policy (SR-07, SR-09) itself.
	RawURL string

	// VanityCode requests a specific code rather than a generated one.
	// Empty means generated. cairn accepts any vanity code that neither
	// collides with the generated length nor appears on the reserved
	// list (ADR-0011) — there is no separate "vanity enabled" switch to
	// configure.
	VanityCode string

	// TTL is this link's time-to-live. Zero means the Shortener's own
	// configured default (which itself defaults to "never expires").
	TTL time.Duration
}

// Create shortens req.RawURL, owned by ownerID.
func (s *Service) Create(ctx context.Context, ownerID string, req CreateRequest) (*cairn.Link, error) {
	opts := []cairn.CreateOption{cairn.WithOwner(ownerID)}
	if req.VanityCode != "" {
		opts = append(opts, cairn.WithVanityCode(cairn.Code(req.VanityCode)))
	}
	if req.TTL > 0 {
		opts = append(opts, cairn.WithTTL(req.TTL))
	}
	return s.shortener.Create(ctx, req.RawURL, opts...)
}

// List returns ownerID's links, oldest first, windowed by after/limit
// (cairn.OwnerLister's own pagination — see cairn's store.go). ownerID
// is taken as given: the caller (Handler) must already have established
// it is the authenticated principal's own ID, never one read from a
// request parameter — ListByOwner is a filter, not authorization
// (cairn's ADR-0010, restated on OwnerLister's own doc comment).
func (s *Service) List(ctx context.Context, ownerID string, after cairn.Cursor, limit int) ([]*cairn.Link, cairn.Cursor, error) {
	return s.lister.ListByOwner(ctx, ownerID, after, limit)
}

// Revoke revokes code, but only if ownerID owns it — the check cairn
// itself deliberately does not perform (ADR-0010). See
// docs/integrations/task-api.md in the cairn module for the exact
// pattern this implements and why: a handler that called
// shortener.Revoke directly would let any authenticated user revoke any
// other user's link, and the missing check is exactly the kind that
// compiles, passes a happy-path test, and survives review.
//
// Resolve — not a separate lookup — is what loads the link: it already
// fails with the same uniform shape SR-03 requires for an invalid,
// unknown, expired or already-revoked code, before ownership is even
// considered. A caller probing for codes by attempting to revoke them
// learns nothing more from this than they would from the public resolve
// route — including when the code exists but belongs to someone else:
// wrong-owner and not-found are the same outcome here for exactly the
// same reason they are everywhere else in this codebase (see
// ErrNotOwner's doc comment). This is also why the 410-vs-authenticated
// distinction docs/INTEGRATION.md's example ErrorEncoder shows is never
// reached through this path — see docs/DECISIONS.md's "Encurtador de
// links" section for the full reasoning.
func (s *Service) Revoke(ctx context.Context, ownerID string, code cairn.Code) error {
	link, err := s.shortener.Resolve(ctx, code)
	if err != nil {
		return err
	}
	if link.OwnerID != ownerID {
		return ErrNotOwner
	}
	return s.shortener.Revoke(ctx, code)
}
