---
paths:
  - 'internal/*/repository.go'
  - 'internal/*/memory_repository.go'
  - 'internal/*/postgres_repository.go'
description: 'The two Repository implementations are one contract with two bodies — they must never disagree'
---

# Repository parity

Each domain package owns exactly one `Repository` interface — the only boundary
its `Service` depends on. It has **two implementations that must answer
identically**:

- `memoryRepository` — not legacy, not a placeholder, not scheduled for
  deletion. It exists so the unit suite has zero external dependencies. Keep it
  correct.
- `postgresRepository` — the production path.

## Adding behaviour

Adding a method to the interface means implementing it in **both** files, in the
same change. A memory implementation that lags behind is a unit suite that
tests something the deployment does not do.

## What must match

- Same sentinel for the same condition (see `go-domain-errors.md`).
- Same ordering. `FindAll` orders by `(created_at, id)` — the memory store
  sorts, the query uses `ORDER BY` plus a matching index.
- Same windowing semantics, including `limit < 0` meaning "no limit"
  (PostgreSQL receives SQL `NULL`, because `LIMIT -1` is an error).
- Same ownership scoping. Methods touching an existing row take the acting
  `userID`; `Update` checks the stored row's owner. A row owned by someone else
  is `ErrNotFound`.

## What may differ

Only *how* a rule is enforced, never *what* it answers:

- Ownership is a `TaskOwnershipFunc` in `attachment.memoryRepository` and a
  `JOIN` (or `INSERT ... SELECT`) in `attachment.postgresRepository`.
- Optimistic concurrency is a compare-and-swap under a mutex in memory, and
  `SELECT ... FOR UPDATE` inside a transaction in PostgreSQL. Both return
  `ErrConflict` to the loser.

## Known divergence — do not add a second one

A malformed (non-UUID) id currently returns `ErrNotFound` from the memory store
and a query error (surfacing as `500`) from PostgreSQL's `::uuid` cast. This is
recorded in `docs/ARCHITECTURE.md` § Future Improvements and pinned by
`TestPostgres_FindByID_MalformedID`. It is the one open exception; the fix is a
shape check **above** both repositories mapping to the same `404`. Do not
introduce a new divergence, and do not paper over this one inside a single
repository.

## Concurrency

Concurrency-sensitive code gets a real concurrent test — actual goroutines
racing each other under `-race` (see `TestConcurrentUpdate_LosersGetErrConflict`
and its PostgreSQL counterpart), not sequential calls asserting the same thing.
