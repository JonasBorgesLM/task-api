---
paths:
  - 'internal/*/errors.go'
  - 'internal/*/handler.go'
  - 'internal/*/service.go'
  - 'internal/*/*_repository.go'
description: 'Sentinel errors per package, checked with errors.Is, mapped to HTTP in exactly one place'
---

# Domain errors

Each domain package owns its sentinels in `errors.go` and they are always
checked with `errors.Is`, never with `==` and never by string matching.

| Package | Sentinels |
|---|---|
| `internal/task` | `ErrNotFound`, `ErrInvalidInput`, `ErrAlreadyExists`, `ErrConflict`, `ErrInvalidTransition` |
| `internal/user` | `ErrNotFound`, `ErrInvalidInput`, `ErrAlreadyExists`, `ErrInvalidCredentials` |
| `internal/attachment` | `ErrNotFound`, `ErrTaskNotFound`, `ErrInvalidInput`, `ErrAlreadyExists`, `ErrTooLarge` |

## Rules

1. **Both `Repository` implementations in a package return the same sentinel
   for the same condition.** A memory/postgres divergence is a bug even when
   both "work" — it means the HTTP status a client sees depends on which
   backend the process happens to be running.
2. **`Handler.handleServiceError` is the only place that maps an error to an
   HTTP status code**, and it must never grow a PostgreSQL-specific branch. If
   you find yourself wanting one, the mapping belongs in the repository, as a
   sentinel.
3. **A resource belonging to another user is `ErrNotFound`**, never a distinct
   "forbidden" error. This is deliberate: a 403 would confirm that the ID
   exists. Preserve it in any new `Repository` method that touches a single
   row.
4. **`errors.Is`, not `==`, even for a sentinel you believe arrives bare.**
   `attachment.Service.Upload`'s `ErrTooLarge` check and `io.EOF` comparisons
   are the current exceptions and they are the pattern to fix, not to copy: a
   `BlobStore` that wraps its error silently turns a 400 into a 500.
5. **Never echo attacker-controlled input back in an error message.** Errors
   land in logs and response bodies. `moat/validate`'s messages describe the
   constraint, not the value; match that.

## Wrapping

Service methods wrap repository errors with a short operation prefix
(`fmt.Errorf("update task: %w", err)`) so a log line says which call failed.
Validation errors are returned **unwrapped** so `handleServiceError` can write
`err.Error()` straight into the `{"error": "..."}` envelope without leaking an
internal call chain to the client.
