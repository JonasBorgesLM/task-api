---
name: go-testing-guide
description: "What to test, at which layer, in which file and under which build tag in task-api — plus the gotchas this specific codebase has already been bitten by. Load before writing or reviewing any test here, and whenever deciding whether a change needs a unit test, an integration test, a contract assertion or a fuzz target."
---

# Testing in task-api

The functional surface of this project is small on purpose; the testing
discipline is the point. This guide is what "tested" means here.

## The five test kinds, and how to tell them apart

| Kind | Build tag | Needs | Lives in | Answers |
|---|---|---|---|---|
| Unit | none | nothing | `service_test.go`, `handler_test.go`, `memory_repository_test.go`, `config_test.go`, `*_test.go` in `internal/middleware` | Does this function do what its doc comment says? |
| Full-stack in-process | none | nothing | `internal/task/integration_test.go` | Do the layers, routes and middleware compose correctly over the real `memoryRepository`? |
| Integration | `//go:build integration` | PostgreSQL / MinIO | `postgres_repository_test.go`, `main_integration_test.go`, `s3_storage_test.go`, `migrate_test.go` | Does the query, the schema, the object store actually behave that way? |
| Interface contract | none (deliberately untagged) | nothing extra | `storage_contract_test.go` | Do **all** implementations keep the same promise? |
| Fuzz | none | nothing | `internal/attachment` | Does the containment property hold for inputs nobody thought of? |

The untagged/tagged distinction is enforced by the build tag alone, never by
naming. `internal/task/integration_test.go` carries no tag and is *not* an
integration test in the CI sense — do not "fix" that by adding one.

## What to test at each layer

### `Service`

The layer that owns business rules, so it owns the majority of the assertions.

- Every validation branch, including the boundary: 200 runes accepted, 201
  rejected — and a **non-ASCII** string at the boundary, because rune-vs-byte
  counting is a bug this codebase actually shipped.
- Every legal transition **and** every illegal one in `legalTransitions`,
  including the no-op self-transition that succeeds independently of the table.
- Normalization: what goes in vs. what is stored (`PlainText` for a title,
  `StripControlChars` for a description — a description's line breaks must
  survive).
- Error identity via `errors.Is`, never by message.
- Timing-sensitive behaviour: `Authenticate` must call bcrypt on the
  unknown-email path too. Assert the call happened, not the wall-clock time.

Use `fakeRepository` to force the paths a real store makes hard to reach: a
repository error, a conflict, a not-found.

### `Handler`

- One test per status code the route can produce. The mapping table in
  `.claude/rules/api-contract.md` is the checklist.
- The `{"error": "..."}` envelope shape, and that a list is `[]` and never
  `null`.
- Query parameter parsing, including the invalid values (`limit=-1`,
  `limit=abc`).
- That `userID` comes from the context and a body/query value cannot override it.

Use `fakeService` — a handler test that needs a real service is testing the
wrong layer.

### `Repository`

Every assertion runs against **both** implementations. The memory version is
untagged, the PostgreSQL version carries the tag, and they assert the same
outcomes:

- ownership scoping (another user's row is `ErrNotFound`, not a distinct error)
- ordering and the `limit`/`offset` window, including `limit < 0`
- optimistic concurrency: a real concurrent test, actual goroutines, under
  `-race`, asserting the losers get `ErrConflict`
- the sentinel returned for each failure condition

### Migrations

Integration-tagged, in `internal/platform/migrate/migrate_test.go`. A new
migration adds a step to the down-migration walk (which steps back **by name**)
and updates the `tableExists` lists. `assertNewestMigrationIs` fails with that
instruction when you forget.

### Middleware

Untagged, and they are cheap — the package sits at 92% for a reason. Test the
composition too, not only each middleware alone: order is load-bearing here (see
`.claude/rules/go-http-handlers.md`).

## Gotchas this repo has already hit

Read `references/gotchas.md` before writing a test in an unfamiliar package.

## Deciding whether a change needs a test

It does, unless it changes no behaviour. The question is only *which file*:

```
muda uma regra de negócio            → service_test.go
muda um status code ou um corpo      → handler_test.go
muda uma query, índice ou escopo     → ambos os *_repository_test.go
muda o schema                        → migrate_test.go (integration)
muda uma promessa de BlobStore       → runBlobStoreContract
muda como uma chave vira um caminho  → o alvo de fuzz
muda só um comentário ou um doc      → nenhum
```

## Running them

```
make test               # unit, no external service
make test-race          # the same under -race — this is what `make check` runs
make test-integration   # needs `make db-up storage-up` first
make fuzz               # path containment; FUZZTIME=5m to go deeper
make coverage           # per-function, unit only
make coverage-full      # true cross-package coverage, unit + integration
```

`make check` is the gate before considering any change done. Never weaken a
check to make something pass — fix the cause.

## Current coverage, for calibration

Unit-only, as of the last measurement: `middleware` 93%, `config` 85%, `user`
74%, `task` 74%, `cmd/api` 70%, `cmd/seed` 41%, `attachment` 42%, `cmd/migrate`
19%, `platform/migrate` 0%. The low numbers are mostly code whose tests are
integration-tagged and therefore invisible to a unit run — check
`make coverage-full` before treating one as a real gap.
