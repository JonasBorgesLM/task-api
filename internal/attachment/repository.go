package attachment

import "context"

// Repository defines the contract for attachment metadata persistence.
// It stores no bytes: the blob behind an Attachment lives outside the
// database, and putting it there is a separate concern from recording
// that it exists.
//
// # Ownership
//
// Every method takes the acting userID and is scoped by it, through the
// task the attachment hangs off. An attachment reachable only via a task
// belonging to somebody else must be reported as ErrNotFound, never as a
// distinct "forbidden" — the same rule internal/task follows, for the
// same reason: a different error for "exists but not yours" tells a
// non-owner that the ID is real.
//
// Scoping belongs here rather than in a Service that fetches and then
// checks, so a PostgreSQL implementation can push the join into the
// query instead of reading rows it is going to discard. It also means
// there is no ordering in which a caller can forget the check.
//
// # Blobs are not cascaded
//
// The database cascades attachment rows when their task is deleted (see
// 0007_create_attachments_table.up.sql). Nothing in that path touches the
// stored bytes, so deleting a task orphans its blobs on disk. That is a
// known gap, not a guarantee this interface makes — a caller that needs
// the bytes gone has to arrange it itself.
// TaskOwnershipFunc reports whether taskID names a task owned by userID.
//
// It exists so that the in-memory Repository can enforce the ownership
// rule without importing internal/task, which the layering forbids: the
// consumer supplies the check, and this package stays unaware that a
// "task" is anything more than an identifier. cmd/api satisfies it from
// the task Repository it already builds.
//
// The PostgreSQL implementation takes no such function — it expresses the
// same rule as a join, which is both cheaper and impossible to skip. That
// asymmetry is in how ownership is *established*, never in what the two
// implementations return: both must answer an attachment on somebody
// else's task with ErrNotFound, and a Create against somebody else's task
// with ErrTaskNotFound.
type TaskOwnershipFunc func(ctx context.Context, taskID, userID string) (bool, error)

type Repository interface {
	// Create records a new attachment. It returns ErrTaskNotFound if
	// taskID does not name a task belonging to userID, which is what
	// prevents an attachment from being hung off somebody else's task.
	Create(ctx context.Context, attachment Attachment, userID string) error

	// FindByStorageKey resolves the identifier a download addresses a
	// file by. Taking userID here is what makes possession of a key
	// insufficient on its own: the key still has to lead to a task the
	// caller owns.
	FindByStorageKey(ctx context.Context, storageKey, userID string) (Attachment, error)

	// FindByTask returns every attachment on one task, ordered
	// deterministically by CreatedAt ascending with ties broken by ID —
	// the same ordering contract task.Repository.FindAll makes, and
	// owned here for the same reason.
	//
	// It exists because without it an upload is write-only: storage keys
	// would be recoverable solely from the response that created them,
	// and a client that lost one would have no way back to its own file.
	// It returns ErrTaskNotFound rather than an empty slice when the
	// task is not the caller's, so "no attachments" and "not your task"
	// stay distinguishable to the caller that is entitled to know.
	FindByTask(ctx context.Context, taskID, userID string) ([]Attachment, error)

	// Delete removes the attachment identified by storageKey, scoped to
	// userID — the same ownership rule FindByStorageKey enforces: a key
	// leading to somebody else's task is reported exactly like a key
	// that names nothing, ErrNotFound either way. Deleting a key that no
	// longer exists (already deleted, by this call or an earlier one) is
	// also ErrNotFound — Repository does not make deletion idempotent on
	// its own terms; Service is what decides how a caller experiences a
	// second delete of the same key, the same split user.Service.Logout
	// keeps between itself and Repository.DeleteSession.
	//
	// This removes only the metadata row. The blob itself is Service's
	// concern (see Service.Delete's doc comment for why, and for what
	// "idempotent" means at that layer) — Repository has no BlobStore to
	// reach one through, by design (see this file's own doc comment on
	// why Repository and BlobStore are separate boundaries).
	Delete(ctx context.Context, storageKey, userID string) error

	// UnreferencedKeys returns those of the given storage keys that no
	// attachment row references.
	//
	// It is the one method here that takes no userID, and the exception
	// is deliberate rather than an oversight: this answers a question
	// about the store as a whole, for the orphan collector, and there is
	// no acting user to scope it to. Scoping it would in fact make it
	// wrong — a key belonging to another user's task is *referenced*,
	// and reporting it as unreferenced would have the collector delete a
	// live attachment.
	//
	// It is an operational method, not a request-path one. Nothing a
	// client can reach should call it.
	UnreferencedKeys(ctx context.Context, keys []string) ([]string, error)
}
