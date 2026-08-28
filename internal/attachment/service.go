package attachment

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JonasBorgesLM/moat/sanitize"
)

// maxOriginalFilenameLen bounds the metadata copy of what the client
// called the file. 255 is the limit essentially every filesystem imposes
// on a single name component, so a longer value could not have been a
// real filename on the uploader's machine either. It matches the VARCHAR
// on the column.
const maxOriginalFilenameLen = 255

// sniffLen is how many leading bytes http.DetectContentType examines. It
// reads at most this many and ignores the rest, so buffering more would
// change nothing.
const sniffLen = 512

// allowedContentTypes is the allow-list, keyed by the type detected from
// the bytes themselves.
//
// It is an allow-list rather than a deny-list because the failure modes
// are not symmetric: a type missing from an allow-list is an upload
// someone has to ask for, while a type missing from a deny-list is an
// upload nobody reviewed. The set is deliberately small — this is a task
// tracker, and every entry is a format a browser will not execute in our
// origin.
//
// text/html is the notable omission, and it is omitted on purpose:
// serving attacker-authored HTML from this API's origin would let it run
// as same-origin script, which is the whole reason downloads also go out
// as Content-Disposition: attachment with nosniff.
var allowedContentTypes = map[string]struct{}{
	"image/jpeg":      {},
	"image/png":       {},
	"image/gif":       {},
	"image/webp":      {},
	"application/pdf": {},
	"text/plain":      {},
}

// Service holds the business logic for attachments: what may be uploaded,
// under what name it is stored, and how the two halves — metadata and
// bytes — are kept consistent with each other.
type Service struct {
	repo            Repository
	blobs           BlobStore
	maxBytes        int64
	maxBytesPerUser int64
	allowed         map[string]struct{}
	nowFunc         func() time.Time
	newIDFunc       func() (string, error)
}

// NewService returns a Service writing metadata through repo and bytes
// through blobs, rejecting any upload over maxBytes, and refusing a new
// upload once userID's total already at or over maxBytesPerUser — see
// Upload's doc comment for exactly when and how that total is checked.
func NewService(repo Repository, blobs BlobStore, maxBytes, maxBytesPerUser int64) *Service {
	return &Service{
		repo:            repo,
		blobs:           blobs,
		maxBytes:        maxBytes,
		maxBytesPerUser: maxBytesPerUser,
		allowed:         allowedContentTypes,
		nowFunc:         time.Now,
		newIDFunc:       newID,
	}
}

// newID generates a random UUID v4, the same way internal/task and
// internal/user do. Duplicated rather than shared because a three-line
// helper is a poorer reason to create a coupling between domain packages
// than it looks.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("failed to generate id: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// idPattern matches the shape newID generates and the shape PostgreSQL's
// ::uuid cast accepts — 8-4-4-4-12 hex, case-insensitive. Duplicated from
// task.Service's identical pattern rather than shared, the same call this
// package already makes for newID: the two domain packages don't import
// each other (see CLAUDE.md), and a one-line regexp isn't worth a shared
// package for.
var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isValidID reports whether id is shaped like a UUID — a taskID or a
// storageKey, both of which are UUIDs by construction (see
// Attachment.StorageKey's doc comment).
//
// Checked before Repository sees either, for the same reason
// task.Service checks it: without this, a malformed value reached
// postgresRepository's `::uuid` cast, which rejected it as a query error
// (500, logged as unexpected) rather than the routine 404 a client-side
// typo deserves — and memoryRepository disagreed with that outcome to
// begin with, answering the same input with ErrNotFound. A malformed
// taskID maps to ErrTaskNotFound (matching Create's own contract for a
// task that doesn't exist or isn't the caller's — see Repository's doc
// comment); a malformed storageKey maps to ErrNotFound, matching an
// unknown key. Neither gets a distinct "malformed" error: that would
// leak that a well-formed ID exists, the same reasoning
// task.Service.isValidID documents.
func isValidID(id string) bool {
	return idPattern.MatchString(id)
}

// Upload stores r as a new attachment on taskID, which must belong to
// userID.
//
// declaredFilename is what the client called the file. It is normalized
// and kept as metadata only — never as a path.
//
// The content type is determined from the bytes, not from what the
// request declared: a client writes that header and can put anything in
// it, so trusting it would make the allow-list decorative. The detected
// type is what gets stored, so a later download describes the bytes it is
// actually serving.
//
// # Per-user quota
//
// Before anything else, Upload checks userID's current total (via
// Repository.TotalBytesForUser) against maxBytesPerUser. A user already
// at or over the quota is refused before a single byte streams in — the
// point of checking first is exactly to avoid streaming a byte in this
// case, not to bound the check's cost.
//
// The total checked is what the user had *before* this upload — the
// new upload's own size isn't known until Put finishes reading the
// stream, so it cannot be part of the number being compared to the
// limit. One accepted upload can therefore push the total up to
// maxBytes past maxBytesPerUser. Checking after the write, once the
// real size is known, would mean writing the blob first and deleting it
// on rejection — pure waste for a ceiling that only has to stop
// sustained abuse, not enforce an exact byte count. See
// docs/DECISIONS.md § "Quota de anexos: por usuário, em bytes" for the
// full reasoning, including why this doesn't also bound concurrent
// uploads racing each other past the quota — the same category of
// accepted, bounded overshoot.
func (s *Service) Upload(ctx context.Context, userID, taskID, declaredFilename string, r io.Reader) (Attachment, error) {
	// Shape-checked before the quota lookup, for the same reason
	// Download/ListByTask check it first: a malformed taskID has no
	// business reaching Repository at all, and TotalBytesForUser is a
	// real query — no reason to pay for it against an id that was never
	// going to resolve to anything.
	if !isValidID(taskID) {
		return Attachment{}, ErrTaskNotFound
	}

	total, err := s.repo.TotalBytesForUser(ctx, userID)
	if err != nil {
		return Attachment{}, fmt.Errorf("upload attachment: %w", err)
	}
	if total >= s.maxBytesPerUser {
		return Attachment{}, fmt.Errorf("%w: attachment quota exceeded (%d/%d bytes used)",
			ErrInvalidInput, total, s.maxBytesPerUser)
	}

	filename, err := normalizeFilename(declaredFilename)
	if err != nil {
		return Attachment{}, err
	}

	// Buffer just enough to identify the content, then hand the reader
	// back its own prefix. Sniffing needs the head of the stream and the
	// store needs all of it, and this is what lets both have it without
	// holding the whole upload in memory.
	//
	// A stream shorter than sniffLen is the normal case, not a failure:
	// io.ReadFull reports io.EOF for an empty reader and
	// io.ErrUnexpectedEOF for a short one, and both mean "that was all
	// of it".
	//
	// errors.Is here is for uniformity with the rest of the package, not
	// for safety: io.ReadFull returns both sentinels directly, and a
	// Reader that wrapped io.EOF would be violating io.Reader's contract
	// — io.Copy, downstream in every BlobStore, would reject it as a
	// real error long before this check mattered. Unlike ErrTooLarge
	// below, where the wrapping case is genuinely reachable.
	head := make([]byte, sniffLen)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return Attachment{}, fmt.Errorf("upload attachment: read: %w", err)
	}
	head = head[:n]

	contentType := detectContentType(head)
	if _, ok := s.allowed[contentType]; !ok {
		// The message names the detected type rather than the declared
		// one, because otherwise a client that mislabels a file gets an
		// error quoting its own incorrect label back and no way to see
		// what the server actually made of the bytes.
		return Attachment{}, fmt.Errorf("%w: content type %q is not accepted", ErrInvalidInput, contentType)
	}

	id, err := s.newIDFunc()
	if err != nil {
		return Attachment{}, fmt.Errorf("upload attachment: %w", err)
	}
	// The storage key is generated here and never derived from anything
	// the client sent. That is what makes the name on disk unable to
	// carry a traversal sequence, and it is also why a repeated upload of
	// the same file cannot overwrite the earlier one.
	storageKey, err := s.newIDFunc()
	if err != nil {
		return Attachment{}, fmt.Errorf("upload attachment: %w", err)
	}

	// Bytes first, metadata second. The reverse order would leave a row
	// pointing at a file that does not exist if the write failed — a
	// download that 500s forever, with nothing to indicate why. This
	// order can leave an unreferenced blob instead, which costs disk and
	// nothing else, and is cleaned up below on the path we can see.
	size, err := s.blobs.Put(ctx, storageKey, io.MultiReader(bytes.NewReader(head), r), s.maxBytes)
	if err != nil {
		// errors.Is, not ==: BlobStore is an interface, and nothing in
		// its contract says an implementation must return ErrTooLarge
		// bare. Both current ones happen to, which is exactly what made
		// == survive here — the day a store wraps it, an upload over the
		// limit stops being a 400 and becomes a 500, with no test
		// failing to say so.
		if errors.Is(err, ErrTooLarge) {
			return Attachment{}, fmt.Errorf("%w: attachment must be at most %d bytes", ErrInvalidInput, s.maxBytes)
		}
		return Attachment{}, fmt.Errorf("upload attachment: %w", err)
	}

	att := Attachment{
		ID:               id,
		TaskID:           taskID,
		OriginalFilename: filename,
		StorageKey:       storageKey,
		ContentType:      contentType,
		SizeBytes:        size,
		CreatedAt:        s.nowFunc(),
	}

	if err := s.repo.Create(ctx, att, userID); err != nil {
		// Best effort: if this fails too, the blob is orphaned. That is
		// the failure this ordering deliberately trades for, and the
		// original error is what the caller needs to see — losing it to
		// report a cleanup problem would hide the actual cause.
		_ = s.blobs.Delete(ctx, storageKey)
		return Attachment{}, fmt.Errorf("upload attachment: %w", err)
	}

	return att, nil
}

// Download resolves storageKey to its metadata and opens the bytes behind
// it. The caller closes the returned reader.
//
// Resolving the key is not authorization: the lookup is scoped to userID,
// so a key that leads to somebody else's task is reported as if it led
// nowhere. See Repository's doc comment.
func (s *Service) Download(ctx context.Context, userID, storageKey string) (Attachment, io.ReadSeekCloser, error) {
	if !isValidID(storageKey) {
		return Attachment{}, nil, ErrNotFound
	}

	att, err := s.repo.FindByStorageKey(ctx, storageKey, userID)
	if err != nil {
		return Attachment{}, nil, fmt.Errorf("download attachment: %w", err)
	}

	blob, err := s.blobs.Open(ctx, att.StorageKey)
	if err != nil {
		// A row whose blob is missing is a real inconsistency, not a
		// caller error — but there is nothing to serve either way, and
		// telling the caller apart from "no such attachment" would only
		// expose an internal fault.
		return Attachment{}, nil, fmt.Errorf("download attachment: %w", err)
	}

	return att, blob, nil
}

// Delete removes the attachment identified by storageKey, scoped to
// userID — the same ownership rule Download enforces: a key leading to
// somebody else's task is reported exactly like a key that names
// nothing, ErrNotFound either way.
//
// # Order: metadata row first, then the blob — deliberately the mirror
// of Upload
//
// Upload writes bytes before the row, because the reverse leaves a row
// pointing at a file that was never written — a download that 500s
// forever. Delete inverts that for the same reason inverted: removing
// the row first means a failure on the step that follows (deleting the
// blob) leaves an orphaned file, which costs disk and nothing else — the
// row-first alternative would instead leave a row pointing at a file
// that is already gone, exactly the broken-reference shape Upload's
// ordering exists to avoid in the first place. See docs/DECISIONS.md §
// "Delete de anexo: síncrono, não só o coletor" for the fuller
// reasoning, including why this doesn't just leave the blob for
// CollectOrphans (#46) to find on its own schedule.
//
// # The blob delete is best-effort
//
// Once the row is gone, the attachment is already gone from the
// caller's perspective — Download and ListByTask stop seeing it. A
// failure removing the underlying blob doesn't change that, so it is
// not reported to the caller as a failed delete; it leaves an orphan
// that CollectOrphans reclaims on its own schedule, the same safety net
// Upload's cleanup relies on for the failure it can't fully undo either.
func (s *Service) Delete(ctx context.Context, userID, storageKey string) error {
	if err := s.repo.Delete(ctx, storageKey, userID); err != nil {
		return fmt.Errorf("delete attachment: %w", err)
	}

	_ = s.blobs.Delete(ctx, storageKey)

	return nil
}

// ListByTask returns every attachment on taskID, which must belong to
// userID.
func (s *Service) ListByTask(ctx context.Context, userID, taskID string) ([]Attachment, error) {
	if !isValidID(taskID) {
		return nil, ErrTaskNotFound
	}

	attachments, err := s.repo.FindByTask(ctx, taskID, userID)
	if err != nil {
		return nil, fmt.Errorf("list attachments: %w", err)
	}
	return attachments, nil
}

// CollectOrphans deletes blobs that no attachment row references and
// that have been sitting untouched for at least minAge. It returns how
// many it removed.
//
// # Why a collector rather than deleting on the spot
//
// Deleting a task cascades its attachment rows away in the database, and
// nothing in that path can reach the filesystem. The alternative — having
// Service delete the blobs as part of the task delete — would couple the
// success of deleting a task to the success of deleting a file, for a
// resource whose loss costs disk space and nothing else. That is the same
// cost reasoning behind writing bytes before metadata in Upload: prefer
// the failure mode that leaves garbage over the one that leaves a broken
// reference or a blocked operation.
//
// # Why minAge is not optional
//
// Upload writes the blob before the metadata row. In the window between
// those two steps a perfectly healthy upload looks exactly like an
// orphan: bytes on disk that no row references. A collector without a
// grace period would race every upload in flight and delete some of them
// — turning a maintenance job into data loss, intermittently and under
// load, which is the worst way to find out.
//
// minAge must therefore exceed the longest plausible gap between those
// two steps: the time to stream a large upload to disk plus the time to
// insert a row. A caller passing zero gets an error rather than a fast
// collector.
//
// # What it will not delete
//
// Only names the store reports that the Repository confirms are
// unreferenced. A name that could not have been written by this
// application — anything that is not a storage key — is not reported as
// unreferenced by Repository.UnreferencedKeys, so a stray file in the
// storage directory is left alone. This function deletes things; every
// ambiguity resolves toward keeping them.
func (s *Service) CollectOrphans(ctx context.Context, minAge time.Duration) (int, error) {
	if minAge <= 0 {
		return 0, fmt.Errorf("%w: orphan collection requires a positive minimum age", ErrInvalidInput)
	}

	refs, err := s.blobs.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("collect orphans: %w", err)
	}

	cutoff := s.nowFunc().Add(-minAge)
	candidates := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.ModTime.Before(cutoff) {
			candidates = append(candidates, ref.Key)
		}
	}
	if len(candidates) == 0 {
		return 0, nil
	}

	orphans, err := s.repo.UnreferencedKeys(ctx, candidates)
	if err != nil {
		return 0, fmt.Errorf("collect orphans: %w", err)
	}

	deleted := 0
	for _, key := range orphans {
		if err := s.blobs.Delete(ctx, key); err != nil {
			// Report what was achieved along with the failure. A pass
			// that removed some files and then hit an I/O error has
			// still done that work, and the caller logging "0 removed"
			// would be wrong about it.
			return deleted, fmt.Errorf("collect orphans: %w", err)
		}
		deleted++
	}

	return deleted, nil
}

// detectContentType identifies the bytes and strips any parameters from
// the result, so that "text/plain; charset=utf-8" is matched against the
// allow-list as "text/plain" — the parameter describes the encoding, not
// the format being allowed.
func detectContentType(head []byte) string {
	full := http.DetectContentType(head)
	if i := strings.IndexByte(full, ';'); i >= 0 {
		full = full[:i]
	}
	return strings.TrimSpace(strings.ToLower(full))
}

// normalizeFilename reduces a client-supplied filename to the metadata
// this package keeps.
//
// It takes only the last path component, because clients do send full
// paths — some browsers historically sent "C:\Users\me\report.pdf" — and
// keeping the directory part would store something that reads like a
// location. That is a display concern rather than a safety one: this
// value never reaches the filesystem, and safety comes from the storage
// key being server-generated. Stripping it anyway means the value is not
// *mistakable* for a path by whatever handles it next.
//
// PlainText strips control characters and collapses whitespace, for the
// same reasons task titles get it: a filename is one line of display
// text, and a name carrying a NUL or an escape sequence damages whatever
// later prints it.
func normalizeFilename(name string) (string, error) {
	// Both separators, regardless of this platform: the value came from
	// the client's machine, not from ours, so which one it uses is not
	// ours to assume.
	name = name[strings.LastIndexAny(name, `/\`)+1:]
	name = sanitize.PlainText(filepath.Base(name))

	// filepath.Base turns an empty or all-separator input into "." or
	// "/", and ".." survives it unchanged — none of the three is a
	// filename anyone typed.
	//
	// Rejecting the dot segments is not a traversal defense: this value
	// never reaches the filesystem, and the name on disk is the
	// server-generated storage key. It is here because the value *is*
	// echoed back in Content-Disposition, and a download offering to
	// save a file as ".." is a nonsense the client should not have to
	// handle.
	switch name {
	case "", ".", "..", string(filepath.Separator):
		return "", fmt.Errorf("%w: filename must not be empty", ErrInvalidInput)
	}
	if utf8.RuneCountInString(name) > maxOriginalFilenameLen {
		return "", fmt.Errorf("%w: filename must be at most %d characters", ErrInvalidInput, maxOriginalFilenameLen)
	}

	return name, nil
}
