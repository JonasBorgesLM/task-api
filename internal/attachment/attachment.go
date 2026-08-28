// Package attachment holds the domain for files attached to a task:
// the Attachment entity, its Repository boundary, and the error sentinels
// callers match on.
//
// An attachment carries no owner of its own. It belongs to a task, and
// that task belongs to a user, so ownership is reached by joining through
// tasks rather than by duplicating a user_id here — a second copy of that
// fact is a second thing that can disagree with the first. Every
// Repository method therefore takes the acting userID and scopes itself
// through that chain; see Repository's doc comment.
package attachment

import "time"

// Attachment is metadata about one file stored against a task. The bytes
// themselves live outside the database, under StorageKey; this package
// knows only where to find them, never how to read them.
type Attachment struct {
	ID     string `json:"id"`
	TaskID string `json:"task_id"`

	// OriginalFilename is what the client called the file. It is
	// metadata and nothing more: it is returned so a download can offer
	// a meaningful name, and it must never be used to build a path. A
	// client-supplied name is attacker-controlled input that can contain
	// separators, traversal sequences, or characters the local
	// filesystem treats specially.
	OriginalFilename string `json:"original_filename"`

	// StorageKey is the name the bytes are actually stored under,
	// generated server-side. It is a UUID, which is what makes it
	// unusable as a traversal vector: nothing that parses as a UUID
	// contains a separator or a dot segment.
	//
	// It is exposed on the wire because the download route addresses
	// files by it. That is not a capability: possession of a key grants
	// nothing on its own, since the download still resolves the key to
	// this row and re-checks that the caller owns the task it hangs off.
	StorageKey string `json:"storage_key"`

	// ContentType is what Service.Upload detected from the bytes
	// themselves (http.DetectContentType), never what the client
	// declared — see validateTitleAndDescription's sibling,
	// detectContentType, for why: a client-declared header would make
	// the upload allow-list decorative. It is stored so a download can
	// echo back the type the server actually determined.
	ContentType string `json:"content_type"`

	// SizeBytes is the size of the stored blob, as measured while
	// writing it rather than as claimed by the request. int64 because
	// the natural 32-bit ceiling of 2 GiB is not a limit this field has
	// any business imposing; what actually bounds an upload is the
	// handler's own limit.
	SizeBytes int64 `json:"size_bytes"`

	CreatedAt time.Time `json:"created_at"`
}
