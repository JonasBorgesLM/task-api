---
paths:
  - 'internal/attachment/**'
description: 'Upload/download/delete safety invariants: detected content type, server-generated keys, bytes-before-metadata (and its mirror on delete), pathguard'
---

# Attachments

Every rule here is load-bearing. Each one has already been decided in
`docs/DECISIONS.md`; a change that contradicts one is a question, not a task.

## Content type

- Decided by `http.DetectContentType` on the **bytes**, never by the client's
  declared header. The allow-list is applied to the detected value; the detected
  value is what gets stored and what a download echoes back.
- A rejected upload is never "fixed" by trusting the declared type — that makes
  the allow-list decorative.
- `text/html` is excluded on purpose: served from this origin it would run as
  same-origin script.

## Keys and paths

- `storage_key` is server-generated (UUID) and never derived from anything the
  client sent. It is an **address, not a permission**: it is on the wire, and
  every lookup that accepts one still re-checks task ownership.
- `original_filename` is metadata only. It is never used to build a filesystem
  path — only echoed in `Content-Disposition`, encoded via
  `mime.FormatMediaType`.
- **Never reach the filesystem except through `pathguard.Guard`.** `Guard.Open`
  and `Guard.Create` are TOCTOU-safe handles. `Resolve` returns a string and
  explicitly is *not* one — `Resolve` + `os.Open` reintroduces the symlink race.

## Write order

Bytes first, metadata row second. The reverse leaves a row pointing at a missing
file — a download that 500s forever. This order leaves at worst an unreferenced
blob. `Service.Upload` cleans that up best-effort and returns the **original**
error, never the cleanup one.

The orphan collector's `minAge` is not optional and must exceed the longest
plausible gap between those two steps: inside that window a healthy in-flight
upload is indistinguishable from an orphan.

## Delete order

The mirror of Write order, deliberately: `Service.Delete` removes the metadata
row **first**, then the blob. A failure on the second step leaves an orphaned
file — cheap, and `CollectOrphans` reclaims it. The reverse would leave a row
pointing at a file that is already gone, the broken-reference shape Write order
exists to avoid.

The blob delete is **best-effort** — its failure is never returned to the
caller. Once the row is gone, the attachment is already gone from the caller's
perspective; reporting a failure at that point would be misleading, not
accurate. See `docs/DECISIONS.md` § "Delete de anexo: síncrono, não só o
coletor".

Deleting an already-deleted key is `ErrNotFound`, not a silent success —
matching `task.Service.DeleteTask`'s contract, not `user.Service.Logout`'s.
Attachment ownership must stay checkable (another user's key is `ErrNotFound`,
never success), which `Logout`'s "no ownership at all" idempotency model
doesn't preserve.

`DELETE /files/{key}` mirrors `GET /files/{key}`'s path shape for the same
reason: nesting under `/tasks/{id}/attachments/{key}` invites the
confused-deputy shape where the path's task and the key's real task disagree.

## Download

- `Content-Disposition: attachment` on **every** download. With the global
  `nosniff` it is what stops user-uploaded bytes rendering in this API's origin.
- `http.ServeContent` (not `io.Copy`) so Range and conditional requests work.
  It sets `Content-Length` itself — do not set it beforehand.

## The BlobStore contract

Two implementations, **one** set of assertions: `runBlobStoreContract`. A new
promise goes there so both backends must keep it. Backend-specific properties
(traversal containment) stay in their own file. See `go-tests.md`.

## Ownership

`internal/attachment` reaches ownership **through the task** and never imports
`internal/task`. Do not add a `user_id` column to `attachments` to "simplify"
this — a second copy of the owner is a second thing that can disagree with the
first.
