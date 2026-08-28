---
name: change-context
description: "Stage 1 of the task-api change pipeline. Consolidates an issue or request into docs/changes/<slug>/context.md — scope, the decisions and invariants it touches, the contract surface, the test surface and the artifacts that must move with it. Use when starting any non-trivial change: 'plan this issue', 'quero implementar X', 'começar a issue #NN'."
disable-model-invocation: true
---

# /change-context

Stage 1. Consolidates sources into a lean `context.md`. It is a **pure
consolidator**: it gathers and states, it does not judge. Detecting conflicts is
`/change-validate`'s job — mixing the two produces a document that argues with
itself.

## Input

`/change-context <slug>` or `/change-context "<free text describing the need>"`

- Argument matches `^[a-z0-9-]+$` → treat as slug.
- Otherwise → derive a kebab-case slug (≤40 chars, stop words stripped) and
  **confirm it with the user** via `AskUserQuestion` before creating any
  directory.
- If `docs/changes/<slug>/` already exists with a `context.md`, ask whether to
  rebuild it or continue from it.

If the user names a GitHub issue number, read it with `gh issue view <NN>` and
use its body as the request text.

## Step 0 — tier assessment (before anything expensive)

Classify per `change-pipeline/SKILL.md` § "When to skip stages" and say so in one
line. If the tier is **trivial**, stop here:

```
Tier: trivial — nada a planejar. Faça a alteração e rode /pre-pr.
```

Do not create a directory for a trivial change.

## Step 1 — dispatch the readers, in parallel

One `Agent` call each, in a single message:

| Subagent | Input |
|---|---|
| `decisions-reader` | the topic of the change, in Portuguese |
| `architecture-reader` | the area of the change |
| `invariants-reader` | the files/packages the change will plausibly touch |
| `openapi-reader` | the affected paths — **only if** the change is wire-visible |
| `test-surface-reader` | each package the change will touch (one call per package, max 3) |

Do not read `CLAUDE.md`, `docs/DECISIONS.md`, `docs/ARCHITECTURE.md` or
`docs/openapi.yaml` yourself. That is what these exist for.

## Step 2 — locate the code

Bounded exploration only:

- `Grep -n` for the symbols and routes the request names.
- Read a whole file **only** when it is the file the change edits and it is under
  ~350 lines. Otherwise grep for the function and read its range.
- Record file:line references. `context.md` points at code; it does not copy it.

## Step 3 — write `docs/changes/<slug>/context.md`

Portuguese prose, English identifiers. Exactly this shape:

```markdown
---
slug: <slug>
stage: context
tier: contained | full
sources_mtime:
  docs/DECISIONS.md: <ISO-8601>
  docs/ARCHITECTURE.md: <ISO-8601>
  CLAUDE.md: <ISO-8601>
  docs/openapi.yaml: <ISO-8601>   # só se wire-visible
---

# <slug> — Contexto

## Pedido

<the request in 3–6 lines, in the user's own terms. If it came from an issue,
cite it: `issue #NN`.>

## Escopo

**Dentro:** <bullets — what this change delivers>
**Fora:** <bullets — what it deliberately does not touch, and why>

## Superfície de código

| Arquivo | Papel na mudança |
|---|---|
| `internal/task/service.go:NNN` | <one line> |

## Decisões registradas que isto toca

<the decisions-reader output, distilled to one line of constraint each, with the
docs/DECISIONS.md line reference. If the reader's Veredito said
sim/possivelmente, state it here verbatim — do not soften it.>

## Invariantes aplicáveis

<the invariants-reader checklist, grouped as it returned them>

## Contrato afetado

<the openapi-reader digest, or `_Nenhum — mudança não visível no contrato._`>

## Superfície de testes

<per package: which file a new test goes in, which build tag, which fakes and
helpers to reuse>

## Artefatos que precisam mudar junto

- [ ] `docs/openapi.yaml` — <why, or "n/a">
- [ ] `README.md` — <why, or "n/a">
- [ ] `CLAUDE.md` — <why, or "n/a">
- [ ] `docs/ARCHITECTURE.md` — <why, or "n/a">
- [ ] `.env.example` — <why, or "n/a">
- [ ] `CHANGELOG.md` — <why, or "n/a">
- [ ] migração `NNNN_*.{up,down}.sql` + `migrate_test.go` — <why, or "n/a">

## Já é um item diferido?

<the architecture-reader answer, verbatim. If yes, the plan must say whether this
change closes that item or is a different thing that happens to be nearby.>

## Perguntas em aberto

<one line each, or `_nenhuma_`. These become AM-N issues at /change-validate.>
```

## Abort conditions

- Slug collides with an existing directory whose scope clearly differs → ask
  before writing anything.
- The request names a route, symbol or config variable that does not exist →
  stop and report; do not invent it.
- The request is a question about the project rather than a change → point at
  `/decide "<need>"` instead.

## Output to the user

The tier, the path written, the count of decisions touched, and — when
`decisions-reader` returned `sim` or `possivelmente` — the conflict, stated
plainly, with the next command:

```
docs/changes/<slug>/context.md escrito. Tier: full.
3 decisões tocadas; 1 possível conflito (ver seção "Decisões registradas").
Próximo: /change-validate <slug>
```
