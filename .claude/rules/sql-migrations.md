---
paths:
  - 'internal/platform/migrate/**'
description: 'Migrations are centralized, embedded, numbered, reversible, and never edited once applied'
---

# Migrations

## Location and ownership

All migrations live in `internal/platform/migrate/migrations/`, **not** inside a
domain package. Both domains share one `*sql.DB`, one `schema_migrations`
bookkeeping table and one embedded runner (`RunMigrations` /
`RunMigrationsDown`).

Files are `NNNN_snake_case_description.{up,down}.sql`, sequentially numbered.
Every `.up.sql` has a matching `.down.sql` that actually reverts it.

## Never edit an applied migration

A schema change is a new numbered pair. Editing `0005` after it has run
anywhere means two databases claiming the same version with different schemas.

## Adding a table

Four things move together, in the same change:

1. The `.up.sql` / `.down.sql` pair.
2. `migrate_test.go` — the down-migration step and the `tableExists` list (see
   `go-tests.md`).
3. Any index the access path needs. PostgreSQL does **not** index a foreign
   key's referencing column automatically; `0006` and `0007` exist because of
   that. Prefer a composite index that serves the filter *and* the ordering
   (`(task_id, created_at, id)`), matching what `FindAll`/`FindByTask` actually
   query.
4. The `docs/openapi.yaml` schema, if the table backs a wire-visible resource.

## What the schema does and does not enforce

- A `CHECK` constraint fences the set of valid **values**. It encodes nothing
  about which **transitions** between them are legal — that is
  `task.Service.legalTransitions`, and it stays there.
- `users.email`'s `UNIQUE` is case-sensitive **on purpose**. Normalization
  (trim + lowercase) is `user.Service`'s job. Do not "fix" it in the schema.
- IDs are `UUID` with no server-side default: `Service` generates them, so
  storage never invents an identifier.
- Timestamps are `TIMESTAMPTZ`, uniformly.

## Comments

Each migration carries a header comment explaining the non-obvious column
choices and why the indexes exist. Match that — and keep it truthful: the
comment on `attachments.content_type` currently says "what the upload declared",
while the code stores the **detected** type. That is the kind of drift these
comments exist to prevent.
