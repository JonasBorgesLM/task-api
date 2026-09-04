package task

import (
	"context"
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JonasBorgesLM/moat/sanitize"
)

// Maximum accepted lengths for user-supplied text fields. These bound
// storage/response size and catch obviously malformed input; they are
// deliberately generous for a task title/description.
const (
	maxTitleLen       = 200
	maxDescriptionLen = 2000
)

// legalTransitions is the complete set of allowed Status transitions,
// keyed by the task's current status. Requesting the task's current
// status again is always allowed as a no-op, independently of this table
// — see TransitionStatus.
//
//	from \ to     pending  in_progress  done  cancelled
//	pending          -          Y        Y        Y
//	in_progress      Y          -        Y        Y
//	done             Y          Y        -        N
//	cancelled        Y          N        N        -
//
// done and cancelled are both reopenable back to pending (and done can
// resume directly to in_progress), but cancelled must pass back through
// pending before becoming active again — this keeps the graph small
// without blocking any realistic workflow. pending -> done stays legal
// directly, matching PATCH /tasks/{id}/done's existing one-hop behavior.
var legalTransitions = map[Status]map[Status]bool{
	StatusPending:    {StatusInProgress: true, StatusDone: true, StatusCancelled: true},
	StatusInProgress: {StatusPending: true, StatusDone: true, StatusCancelled: true},
	StatusDone:       {StatusPending: true, StatusInProgress: true},
	StatusCancelled:  {StatusPending: true},
}

// validStatuses is the complete set of Status values a caller may name as
// a TransitionStatus target.
var validStatuses = map[Status]bool{
	StatusPending:    true,
	StatusInProgress: true,
	StatusDone:       true,
	StatusCancelled:  true,
}

// validPriorities is the complete set of Priority values CreateTask/
// UpdateTask accept.
var validPriorities = map[Priority]bool{
	PriorityLow:    true,
	PriorityMedium: true,
	PriorityHigh:   true,
}

// Service holds the business logic for task management.
type Service struct {
	repo Repository
}

// NewService returns a new Service with the given Repository.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// newID generates a random UUID v4.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("failed to generate task ID: %w", err)
	}

	// Set version 4 (bits 4–7 of byte 6).
	b[6] = b[6]&0x0f | 0x40
	// Set variant bits (bits 6–7 of byte 8 to 0b10).
	b[8] = b[8]&0x3f | 0x80

	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:],
	), nil
}

// idPattern matches the shape newID generates and the shape PostgreSQL's
// ::uuid cast accepts — 8-4-4-4-12 hex, case-insensitive. It is
// deliberately not a strict version-4 check: a valid RFC 4122 UUID of any
// version reaches this far unrejected today (via the ::uuid cast), and
// tightening the check beyond "is this a UUID at all" would reject input
// PostgreSQL itself would have accepted.
var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isValidID reports whether id is shaped like a UUID.
//
// Called at the top of every Service method that takes a caller-supplied
// task ID, before Repository ever sees it. Without this, a malformed ID
// reached postgresRepository's `::uuid` cast, which rejected it as a
// query error — surfacing as 500, and logged as an unexpected failure,
// for what is actually a routine case of client-supplied garbage. Worse,
// memoryRepository disagreed: the same input returned ErrNotFound (404)
// there, so which status a client saw depended on which backend happened
// to be running. Checking here, above both, is what makes them agree —
// see TestPostgres_FindByID_MalformedID and its memory counterpart.
//
// A malformed ID maps to ErrNotFound, not a distinct "malformed ID"
// error: this package's rule for a task belonging to someone else is
// ErrNotFound rather than a shape that would confirm a well-formed ID
// exists (see Repository's doc comment) — a malformed one gets the exact
// same treatment, for the same reason.
func isValidID(id string) bool {
	return idPattern.MatchString(id)
}

// CreateTask validates input, builds a new Task owned by userID and
// persists it. An empty priority defaults to PriorityMedium; otherwise it
// must be one of low/medium/high.
func (s *Service) CreateTask(ctx context.Context, userID, title, description, priority string) (Task, error) {
	title, description, err := validateTitleAndDescription(title, description)
	if err != nil {
		return Task{}, err
	}

	p, err := validatePriority(priority, PriorityMedium)
	if err != nil {
		return Task{}, err
	}

	id, err := newID()
	if err != nil {
		return Task{}, fmt.Errorf("create task: %w", err)
	}

	now := time.Now()
	task := Task{
		ID:          id,
		UserID:      userID,
		Title:       title,
		Description: description,
		Status:      StatusPending,
		Priority:    p,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.repo.Create(ctx, task); err != nil {
		return Task{}, fmt.Errorf("create task: %w", err)
	}

	return task, nil
}

// GetTask retrieves userID's Task by its ID.
func (s *Service) GetTask(ctx context.Context, userID, id string) (Task, error) {
	if !isValidID(id) {
		return Task{}, ErrNotFound
	}

	task, err := s.repo.FindByID(ctx, id, userID)
	if err != nil {
		return Task{}, fmt.Errorf("get task: %w", err)
	}
	return task, nil
}

// ListTasks returns userID's stored tasks ordered deterministically by
// CreatedAt (oldest first) and then by ID to break ties, optionally
// windowed to at most limit results starting at offset. limit < 0 means
// "no limit" — every task from offset onward is returned.
//
// status and priority are the caller's raw, unvalidated query values; an
// empty string means "no filter on this field" (the same "not provided"
// sentinel CreateTask/UpdateTask's priority already uses), and a non-empty
// value that doesn't name a known Status/Priority returns ErrInvalidInput
// without touching Repository. Both filters combine with AND when present.
//
// The ordering and the limit/offset window are both enforced by
// Repository.FindAll itself (see its doc comment), not re-derived here:
// pushing them down to storage means a PostgreSQL-backed Repository can
// apply ORDER BY/LIMIT/OFFSET in the query instead of fetching every row
// into the process on every call just to discard most of them. The
// status/priority filters follow the same reasoning — see FindAll.
func (s *Service) ListTasks(ctx context.Context, userID string, limit, offset int, status, priority string) ([]Task, error) {
	st, err := validateStatusFilter(status)
	if err != nil {
		return nil, err
	}
	p, err := validatePriorityFilter(priority)
	if err != nil {
		return nil, err
	}

	tasks, err := s.repo.FindAll(ctx, userID, limit, offset, st, p)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	return tasks, nil
}

// UpdateTask updates the title, description and (optionally) priority of
// an existing Task owned by userID. CreatedAt and Status are never
// modified here — see TransitionStatus for status changes. UpdatedAt is
// set to now. An empty priority leaves the task's current priority
// unchanged; otherwise it must be one of low/medium/high.
//
// UpdateTask performs a read-modify-write against the Repository with
// optimistic concurrency control: it passes back the Version it read from
// FindByID, and Repository.Update rejects the write with ErrConflict if
// another writer updated the same task in between. The caller should treat
// ErrConflict as retryable — re-read the task and try again.
func (s *Service) UpdateTask(ctx context.Context, userID, id, title, description, priority string) (Task, error) {
	if !isValidID(id) {
		return Task{}, ErrNotFound
	}

	title, description, err := validateTitleAndDescription(title, description)
	if err != nil {
		return Task{}, err
	}

	task, err := s.repo.FindByID(ctx, id, userID)
	if err != nil {
		return Task{}, fmt.Errorf("update task: %w", err)
	}

	p, err := validatePriority(priority, task.Priority)
	if err != nil {
		return Task{}, err
	}

	task.Title = title
	task.Description = description
	task.Priority = p
	task.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, task); err != nil {
		return Task{}, fmt.Errorf("update task: %w", err)
	}

	return task, nil
}

// DeleteTask removes userID's Task by its ID.
func (s *Service) DeleteTask(ctx context.Context, userID, id string) error {
	if !isValidID(id) {
		return ErrNotFound
	}

	if err := s.repo.Delete(ctx, id, userID); err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	return nil
}

// TransitionStatus moves userID's Task with the given id to target,
// enforcing legalTransitions. Requesting the task's current status again
// is always a no-op success (idempotent, and never calls Repository.Update
// — so it cannot ErrConflict), the same idempotency CompleteTask has
// always provided for "already done". Any other transition not present in
// legalTransitions[current] returns ErrInvalidTransition without applying
// the write.
//
// Like UpdateTask, this uses optimistic concurrency control via the
// Version read from FindByID; see UpdateTask's doc comment.
func (s *Service) TransitionStatus(ctx context.Context, userID, id string, target Status) (Task, error) {
	if !isValidID(id) {
		return Task{}, ErrNotFound
	}
	if !validStatuses[target] {
		return Task{}, fmt.Errorf("%w: unknown status %q", ErrInvalidInput, target)
	}

	task, err := s.repo.FindByID(ctx, id, userID)
	if err != nil {
		return Task{}, fmt.Errorf("transition status: %w", err)
	}

	if task.Status == target {
		return task, nil
	}
	if !legalTransitions[task.Status][target] {
		return Task{}, fmt.Errorf("%w: cannot move from %q to %q", ErrInvalidTransition, task.Status, target)
	}

	task.Status = target
	task.UpdatedAt = time.Now()

	if err := s.repo.Update(ctx, task); err != nil {
		return Task{}, fmt.Errorf("transition status: %w", err)
	}

	return task, nil
}

// CompleteTask marks userID's Task as done. It is a thin, backward-compatible
// wrapper around TransitionStatus(..., StatusDone) — kept as its own method
// because PATCH /tasks/{id}/done predates the general status endpoint and
// its wire contract (200, unchanged body shape) must not change.
func (s *Service) CompleteTask(ctx context.Context, userID, id string) (Task, error) {
	task, err := s.TransitionStatus(ctx, userID, id, StatusDone)
	if err != nil {
		return Task{}, fmt.Errorf("complete task: %w", err)
	}
	return task, nil
}

// validateTitleAndDescription normalizes title and description and
// validates their length: title must be non-empty (after normalizing) and
// at most maxTitleLen characters; description must be at most
// maxDescriptionLen characters. It returns the normalized values, or
// ErrInvalidInput describing which constraint failed.
//
// # Normalization
//
// The two fields are normalized differently on purpose. A title is one
// line of display text, so sanitize.PlainText applies: it strips Unicode
// control characters and collapses every run of whitespace to a single
// space. A description is free-form prose where line breaks are content,
// so it only gets sanitize.StripControlChars — running PlainText over it
// would silently flatten a multi-paragraph description into one line,
// destroying data the user deliberately entered.
//
// Control characters are stripped rather than rejected because they are
// almost never typed on purpose: they arrive from a paste, a copied
// terminal buffer, or a client that failed to strip them. What they cost
// once stored is real — a NUL truncates the value for anything that later
// treats it as a C string, and a raw escape sequence can rewrite a
// terminal that prints a log line containing it. Tab, newline and
// carriage return are preserved, which is what makes a description's
// formatting survive.
//
// What this does *not* cover, so that nobody reads more into it than is
// there: only Unicode category Cc (the C0/C1 controls) is stripped.
// Format characters in category Cf survive, and that includes the
// bidirectional overrides (U+202A–U+202E) and isolates (U+2066–U+2069)
// behind Trojan Source-style spoofing, where a stored title renders in a
// different order than it reads in the source. Handling those is not a
// matter of extending the strip set: the isolates are the correct way to
// embed Hebrew or Arabic in surrounding Latin text, so removing them
// would corrupt legitimate input from exactly the users who need them.
// It needs its own rule about which of those characters are acceptable
// where, which this project has not made yet.
//
// This is also deliberately not an HTML defense. Nothing here strips
// tags, and output encoding at the point of rendering remains the only
// real answer to injection; a title of "Ben <the> Third" is stored
// exactly as typed.
//
// # Length
//
// Length is measured in Unicode characters (utf8.RuneCountInString), not
// bytes: len() on a Go string counts bytes, which for any non-ASCII text
// (accents, Cyrillic, CJK, emoji, ...) is larger than — and therefore a
// stricter limit than — the character count the error message and the
// PostgreSQL VARCHAR(200)/VARCHAR(2000) columns (which count characters,
// not bytes) actually enforce. Measuring bytes here would reject valid
// input well under the intended limit.
//
// Length is checked *after* normalization, so the limit applies to what
// is actually stored. Checking first would reject input that fits once
// its stripped characters are gone.
func validateTitleAndDescription(title, description string) (string, string, error) {
	// PlainText already trims, so TrimSpace would be redundant on title.
	title = sanitize.PlainText(title)
	description = strings.TrimSpace(sanitize.StripControlChars(description))

	if title == "" {
		return "", "", fmt.Errorf("%w: title must not be empty", ErrInvalidInput)
	}
	if utf8.RuneCountInString(title) > maxTitleLen {
		return "", "", fmt.Errorf("%w: title must be at most %d characters", ErrInvalidInput, maxTitleLen)
	}
	if utf8.RuneCountInString(description) > maxDescriptionLen {
		return "", "", fmt.Errorf("%w: description must be at most %d characters", ErrInvalidInput, maxDescriptionLen)
	}

	return title, description, nil
}

// validatePriority returns fallback if priority is empty (the "not
// provided" sentinel both CreateTask and UpdateTask use — a real Priority
// value is never the empty string), or priority itself if it names a
// valid Priority. Otherwise it returns ErrInvalidInput.
func validatePriority(priority string, fallback Priority) (Priority, error) {
	if priority == "" {
		return fallback, nil
	}
	p := Priority(priority)
	if !validPriorities[p] {
		return "", fmt.Errorf("%w: priority must be one of low, medium, high", ErrInvalidInput)
	}
	return p, nil
}

// validateStatusFilter returns "" (no filter) if status is empty, or
// status itself if it names a known Status; otherwise ErrInvalidInput.
// Unlike validatePriority, there is no fallback value to substitute — an
// empty filter stays empty, it does not default to some particular status.
func validateStatusFilter(status string) (Status, error) {
	if status == "" {
		return "", nil
	}
	s := Status(status)
	if !validStatuses[s] {
		return "", fmt.Errorf("%w: status must be one of pending, in_progress, done, cancelled", ErrInvalidInput)
	}
	return s, nil
}

// validatePriorityFilter is validatePriority's counterpart for filtering:
// it returns "" (no filter) if priority is empty, or priority itself if it
// names a known Priority; otherwise ErrInvalidInput. Kept separate from
// validatePriority because that function's empty-string case defaults to a
// caller-supplied fallback Priority (medium on create, the existing value
// on update) — semantics that don't apply to "no filter on this field".
func validatePriorityFilter(priority string) (Priority, error) {
	if priority == "" {
		return "", nil
	}
	p := Priority(priority)
	if !validPriorities[p] {
		return "", fmt.Errorf("%w: priority must be one of low, medium, high", ErrInvalidInput)
	}
	return p, nil
}
