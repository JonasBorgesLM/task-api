---
paths:
  - 'internal/**/*.go'
  - 'cmd/**/*.go'
description: 'One-way dependency Handler → Service → Repository, and what may know that PostgreSQL exists'
---

# Layering

`Handler → Service → Repository (interface)`. Each layer knows only the layer
directly below it, and only through an interface. This holds **identically** in
`internal/task`, `internal/user` and `internal/attachment` — there is no
"original" package with more privileges than the others.

`internal/link` (issues #209-#217) is the one deliberate exception to
"Repository (interface)," not a fourth copy of the same shape: it wraps
`*cairn.Shortener`, and cairn itself — not `internal/link`'s own
`Service` — is the Service-layer domain logic here, with its own
storage (`cairn.Store`, currently `memstore`; see `docs/DECISIONS.md` §
"Encurtador de links"). `link.Service`'s entire job is the one thing
cairn deliberately does not do: the ownership check on `Revoke`
(cairn's own ADR-0010). `link.Handler` still never touches
`*cairn.Shortener` directly — same rule, enforced through `Service` the
same way it is everywhere else — and cairn's own sentinel errors
(`cairn.ErrCodeNotFound`, etc.) are this package's domain-error
taxonomy; it does not wrap them in a second, redundant sentinel layer.

## What may import a database

- `Service` and `Handler` in every domain package must stay **completely
  unaware that PostgreSQL exists**. No `database/sql`, no `pgx`, no SQL string,
  no SQLSTATE branch.
- Only `cmd/api`, `cmd/migrate`, `cmd/seed`, each package's
  `postgres_repository.go`, and `internal/platform/migrate` may import a
  PostgreSQL package.
- `cmd/api/newServer` and `openDatabase` are the **only** place that decides
  which `Repository` implementation backs the process. Same for
  `buildBlobStore` and `BlobStore`.

## Composition root

`cmd/api` is the composition root and the only assembler of cross-package
types. Two consequences that are easy to get wrong:

- `internal/config` imports **nothing outside the standard library**. Values
  arrive as flat fields (`AttachmentS3Endpoint`, `AttachmentS3Bucket`, …) and
  `cmd/api` assembles them into `attachment.S3Config`. Do not make `Config`
  hold a domain type; do not import `moat` from `config`.
- `internal/middleware` has **no domain knowledge** — it never imports
  `internal/task`, `internal/user` or `internal/attachment`. Domain-aware
  middleware lives beside its domain (`user.RequireAuth`) and calls
  `middleware`'s generic context helpers, never the reverse.

## Cross-domain coupling

Domain packages do not import each other. Where a rule genuinely spans two of
them, the composition root supplies the seam:

- `internal/attachment` reaches ownership **through the task** and never
  imports `internal/task`. `memoryRepository` takes a `TaskOwnershipFunc` that
  `cmd/api` satisfies from the task `Repository` it already built;
  `postgresRepository` expresses the same rule as a `JOIN`.
- Three-line helpers (`newID`) are duplicated per package on purpose. That
  duplication is cheaper than the coupling a shared package would create — do
  not "DRY" it into `internal/platform`.

## One-off operational tools

A new operational CLI gets its own `cmd/<name>` binary: a small `main.go` that
loads `config.Config`, opens its own `*sql.DB`, and drives the domain packages'
**exported** API. Never a flag or an endpoint bolted onto `cmd/api`.

`cmd/seed` sets the standard: it writes exclusively through `user.Service` /
`task.Service` (`Register`, `CreateTask`, `TransitionStatus`), never straight to
a `Repository`, so seeded data is subject to the same validation, ID generation
and timestamp rules as data created through the API.
