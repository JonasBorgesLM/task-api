---
name: contract-sync
description: "Audit task-api for drift between the code and the documents that describe it — handlers vs docs/openapi.yaml, config fields vs .env.example and the README table, Makefile targets vs CI, invariants in CLAUDE.md vs what the code actually does. Use before a PR, after a feature lands, or when asked whether the docs are still true."
---

# /contract-sync

This repository's documents are load-bearing — `CLAUDE.md` states that a change
to a `Repository` interface, a config variable or a `make` target updates the
docs in the **same** change. This skill finds the places where that did not
happen.

It is a **read-and-report** skill. It proposes fixes; it does not apply them
unless the user asks.

## Scope — five axes

Run all five. Each is mechanical; none needs a whole file loaded.

### 1. Routes ↔ `docs/openapi.yaml`

```bash
grep -rn 'mux.Handle("' internal/*/handler.go
grep -rn 'mux.Handle\|HandleFunc' cmd/api/main.go cmd/api/health.go
grep -n '^  /' docs/openapi.yaml
```

Compare the two sets, remembering the mount: handlers register **unprefixed**
patterns and `newServer` mounts them under `/v1`, while `/health`,
`/health/ready` and `/debug/vars` are registered bare and documented bare.

Report: routes with no documented path; documented paths with no route; a method
documented for a path the mux does not register.

Then, for each route that changed recently (`git log -p --since` on the handler),
use `openapi-reader` on its path and check the **status codes** the handler can
actually produce against the documented set.

### 2. Config ↔ `.env.example` ↔ README

```bash
grep -n 'os.Getenv("' internal/config/config.go
grep -n '^#\?[A-Z_]*=' .env.example
grep -n '^| `[A-Z_]*`' README.md
```

Every variable must appear in all three, with the **same default**. Report any
that appears in fewer, and any default that disagrees between the code's
`default*` constant and what the README table or `.env.example` claims.

Also check the deployment surfaces: a variable that a deployment needs should
appear in `docker-compose.yml` or `k8s/30-config.yaml`, and a service started
there should actually be reachable by the config the API is given.

### 3. Make targets ↔ CI ↔ README

```bash
grep -n '^[a-z-]*:' Makefile
grep -n 'run:' .github/workflows/ci.yml
```

`make check`'s help text claims it runs "everything the CI quality gate runs".
Check that claim: list what CI runs that `check` does not, and vice versa. Report
each difference with whether it is deliberate (integration tests need services)
or drift.

### 4. `CLAUDE.md` invariants ↔ reality

For each invariant `CLAUDE.md` states as a fact about the code, find the cheapest
mechanical check and run it. The high-value ones:

- "Runtime dependencies are pgx, x/crypto and moat" → `go list -m all`, and
  check the `require` blocks in `go.mod` distinguish direct from indirect
  correctly (`go mod tidy -diff`).
- "`internal/config` imports nothing outside the standard library" →
  `go list -deps ./internal/config`
- "`Service` and `Handler` are unaware PostgreSQL exists" →
  `grep -rn 'database/sql\|pgx' internal/*/service.go internal/*/handler.go`
- "`middleware` never imports a domain package" →
  `grep -rn 'internal/task\|internal/user\|internal/attachment' internal/middleware/`
- "Every download keeps `Content-Disposition: attachment`" →
  `grep -n 'Content-Disposition' internal/attachment/handler.go`

Report any invariant the code no longer satisfies, and any invariant that has
become **stale** — still true, but describing a smaller world than the code now
has.

### 5. Comments ↔ code

Doc comments here carry real constraints, which means a stale one actively
misleads. Check the highest-stakes ones: a comment that describes a security
property (what is trusted, what order things happen in, what is validated) must
match the code beside it. Migration header comments are included — they document
column semantics that the code decides.

## Output

```markdown
## contract-sync — <date>

### Divergências encontradas

| # | Eixo | Onde | O que está errado | Correção |
|---|---|---|---|---|
| 1 | config | `.env.example` | `X` documentado com default `a`, código usa `b` | alinhar `.env.example` |

### Eixos sem achados
- Rotas ↔ openapi.yaml — ok

### Correções sugeridas, em ordem
1. <one line each, smallest first>
```

Report divergences plainly, with the file:line on both sides. Do not soften a
finding into "could be improved" when the two documents literally disagree —
say which one is wrong, or that it cannot be determined without the user.
