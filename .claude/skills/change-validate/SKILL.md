---
name: change-validate
description: "Stage 2 of the task-api change pipeline. Reads docs/changes/<slug>/context.md and checks the planned change against recorded decisions, CLAUDE.md invariants, repository parity, contract coverage, test coverage and doc drift — producing validation.md with a clean|dirty verdict that gates /change-plan."
disable-model-invocation: true
---

# /change-validate

Stage 2. Turns the consolidated context into a list of **issues with IDs** and a
single verdict. `/change-plan` refuses to run while the verdict is `dirty`.

Rerun this after resolving issues — it regenerates `validation.md` in place,
carrying resolved issues to the bottom with their IDs intact.

## Preflight (O(1), before any reading)

1. `docs/changes/<slug>/context.md` exists → else abort:
   `"Falta context.md para <slug>. Rode /change-context <slug> primeiro."`
2. `sources_mtime` is fresh → else abort with the staleness message from
   `change-pipeline/SKILL.md`.
3. Read `context.md` in full. It is the only file this stage reads whole.

## The eight checks

Run every one. A check that finds nothing still gets a line saying so — silence
is indistinguishable from "not run".

### 1. Decision conflicts → `DC-N`

For every decision listed in context's "Decisões registradas", ask: does the
planned change do the thing that decision forbids? Look specifically for the
reversals the project has already argued about:

- inverting the write order in `attachment.Service.Upload`
- trusting the client's declared `Content-Type`
- adding `user_id` to `attachments`
- swapping opaque session tokens for JWT, or adding a second auth mechanism
- switching `moat/ratelimit` for a distributed store without discussion
- adding `includeSubDomains`/`preload` to HSTS, or loosening the CSP
- an unversioned alias or redirect for a `/v1` path
- reading `X-Forwarded-For` without `TRUSTED_PROXIES`
- short-circuiting the dummy bcrypt comparison in `user.Service.Authenticate`

**`DC-N` is blocking and can only be closed by the user.** Never resolve one by
reasoning that the decision was probably fine to break.

### 2. Invariant conflicts → `IV-N`

Against the invariants checklist in context, and `CLAUDE.md` § "Things not to do
without being asked". Layering violations, a domain package importing another, a
PostgreSQL branch reaching `Service` or `handleServiceError`, a new dependency,
`internal/config` importing outside the stdlib. **Blocking.**

### 3. Contract coverage → `CG-N`

If any handler's observable behaviour changes and the artifact checklist does not
have `docs/openapi.yaml` checked, that is a `CG-N`. Also: a new status code that
is not in `api-contract.md`'s settled table, or a new error condition with no
documented code.

### 4. Repository / BlobStore parity → `PG-N`

If the change touches `memory_repository.go` without `postgres_repository.go`
(or vice versa), or adds a `BlobStore` promise without putting it in
`runBlobStoreContract`, or adds a `Repository` method that appears in one
implementation only.

### 5. Test coverage → `TG-N`

Every behaviour change needs a test at the layer that owns it. Specifically:

- new validation or transition rule → `service_test.go`
- new status code or body shape → `handler_test.go`
- new query, index or ownership scoping → `postgres_repository_test.go`
  (`//go:build integration`) **and** its memory counterpart
- new concurrency-sensitive path → a real goroutine-racing test under `-race`
- new migration → the down-migration step in `migrate_test.go`
- new `BlobStore` promise → `runBlobStoreContract`

### 6. Doc drift → `DD-N`

Any unchecked box in context's "Artefatos que precisam mudar junto" that the
change actually requires. Plus: a new env var missing from `.env.example` or the
README table; a client-visible change missing from `CHANGELOG.md`; a breaking
change not marked **BREAKING**.

### 7. Ambiguity → `AM-N`

Any open question from context, and any place where the request admits two
readings that produce different code. State both readings explicitly — an
ambiguity the reader has to reconstruct is not resolved by writing it down.

### 8. Already deferred → `AD-N`

If `architecture-reader` matched a Future Improvements bullet. This is
**advisory, never blocking** — it does not flip the verdict. It exists so the
plan says whether this change closes that item (and therefore removes the
bullet) or is merely adjacent to it.

## Output — `docs/changes/<slug>/validation.md`

```markdown
---
slug: <slug>
stage: validation
status: clean | dirty
open_count: N
issues:
  - id: DC-1
    status: open
    blocking: true
    summary: "<≤80 chars>"
  - id: TG-1
    status: resolved
    blocking: false
    summary: "<≤80 chars>"
    resolved_by: "<what closed it>"
sources_mtime:
  docs/changes/<slug>/context.md: <ISO-8601>
  docs/DECISIONS.md: <ISO-8601>
  CLAUDE.md: <ISO-8601>
---

# <slug> — Validação

## Veredito

**status: dirty** — N questões abertas, M delas bloqueantes.

## Questões abertas

### DC-1 — <headline>  *(bloqueante)*

- **O que a mudança faria:** <one line>
- **O que está registrado:** <the decision, with docs/DECISIONS.md:NN>
- **Por que bloqueia:** <one line>
- **Como fechar:** decisão do usuário. Ou (a) <alternative that respects the
  decision>, ou (b) mudar a decisão via `/decide` e registrar o novo trade-off.

### TG-1 — <headline>

- **Lacuna:** <one line>
- **Como fechar:** <the concrete test file and what it must assert>

## Checagens sem achados

- Paridade de Repository/BlobStore — ok
- ...

## Questões resolvidas

### DD-1 — <headline>
- **Fechada por:** <what closed it>
```

`status: clean` requires **zero** open issues in every blocking and
non-blocking category except `AD-N`, which never affects the verdict.

## Output to the user

The verdict, the blocking issues **spelled out** (not just counted), and the next
command:

- dirty with blocking issues → present them and ask; do not proceed.
- dirty with only non-blocking issues → say they will be closed by planning the
  missing work, and point at `/change-plan <slug>` after a rerun.
- clean → `"validation.md: clean. Próximo: /change-plan <slug>"`
