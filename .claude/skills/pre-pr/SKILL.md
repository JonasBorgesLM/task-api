---
name: pre-pr
description: "The gate before opening a pull request in task-api: runs the full quality gate, verifies the docs moved with the code, checks the changelog and the branch, and produces the PR description. Use for 'abrir PR', 'está pronto para revisão?', 'rodar o gate', or at the end of /implement-change."
---

# /pre-pr

The definition of done, executed rather than remembered. Nothing here is
optional and nothing is reported as passing without having been run.

## 1. Branch and scope

```bash
git branch --show-current
git status --short
git diff --stat main...HEAD
```

- Not on `main` or `develop`. The repo's prefixes are `feature/`, `fix/`,
  `docs/`, `ci/`.
- The diff contains **one** scope. Cosmetic reformatting mixed into a functional
  change is a separate commit — say so rather than shipping it.
- No `.env`, no `coverage.out`, no `bin/`, no editor files. `git status
  --ignored` if anything looks suspicious.

## 2. The quality gate

```bash
make check
```

That is gofmt-check, `go vet` (default **and** `-tags=integration`),
`staticcheck` (both), `govulncheck`, and race-tested unit tests. Report the real
result. A failure stops the gate — fix the cause, never the check.

If the diff touched a repository, a migration, a blob store or `cmd/`:

```bash
make db-up storage-up
make test-integration
```

If it touched key generation or blob reads:

```bash
make fuzz
```

## 3. Module hygiene

```bash
go mod tidy -diff
go mod verify
```

`tidy -diff` must produce nothing. A non-empty diff means `go.mod` misclassifies
a direct dependency as indirect, or carries one nothing imports — run
`make tidy` and commit the result.

A **new** dependency needs more than a green build: it must be pure Go (the
`scratch` image depends on `CGO_ENABLED=0`), and the reason it was chosen belongs
in `docs/DECISIONS.md`. Say which section records it.

## 4. Documents that must have moved

Check each against the diff. An unticked box with no `n/a` blocks the PR.

- [ ] `docs/openapi.yaml` — any handler behaviour change: new field, new status,
      new parameter, new route
- [ ] `README.md` — new route in the endpoint table, new config var in the
      configuration table, new `make` target
- [ ] `CLAUDE.md` — a changed or added invariant, a new "do not do this without
      asking"
- [ ] `docs/ARCHITECTURE.md` — a design change, or a Future Improvements bullet
      this change closes (remove it) or creates (add it)
- [ ] `docs/DECISIONS.md` — a non-obvious choice, recorded via `/decide`
- [ ] `.env.example` — a new or renamed environment variable
- [ ] `CHANGELOG.md` — anything a client can observe, under the right heading,
      marked **BREAKING** when an existing caller stops working
- [ ] `.claude/rules/*.md` — a new invariant that should fire automatically on
      the next edit to that kind of file

Run `/contract-sync` if the change was broad enough that you cannot answer these
from the diff alone.

## 5. Review the diff as a reviewer would

```bash
git diff main...HEAD
```

Look specifically for what this codebase cares about:

- A doc comment on every new exported symbol, recording a **why** — a constraint
  or a rejected alternative, not a restatement of the signature.
- Errors as sentinels, checked with `errors.Is`, wrapped with an operation prefix.
- Both `Repository` implementations moved together; a new `BlobStore` promise in
  `runBlobStoreContract`.
- No speculative abstraction: a config option, interface or layer added for a
  need that does not exist yet.
- No debug leftovers, no commented-out code, no TODO without an issue.

## 6. The PR description

Portuguese, matching the repo's commits and changelog. Short and about the
**why**:

```markdown
## O que muda

<2–4 lines>

## Por quê

<the problem, and the alternative rejected if there was one>

## Como foi verificado

- `make check` — ok
- `make test-integration` — ok (N testes)
- <manual verification, if any>

## Documentação

- <the files updated, one line each>

## Decisões registradas

- <docs/DECISIONS.md section, or "nenhuma">
```

## Report

State the gate result honestly and completely: what ran, what passed, what
failed with its actual output, and every box that is not ticked. A change with a
failing check or an unticked deliverable is **not** ready — say that plainly
rather than listing it as a caveat under a "ready" verdict.
