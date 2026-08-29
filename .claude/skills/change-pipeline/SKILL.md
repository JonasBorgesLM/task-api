---
name: change-pipeline
description: "Entry point and shared conventions for the task-api change pipeline (context → validate → plan → implement). Invoke when the user asks how to plan or organize work in this repo without naming a stage. For actual work use the stage skills: change-context, change-validate, change-plan, implement-change."
disable-model-invocation: true
---

# Change pipeline — overview

task-api is a mature, production-shaped repository with a settled architecture,
a recorded decision log and a versioned contract. Work does not arrive as
greenfield phases; it arrives as an **issue** — a feature, a fix, a hardening
pass, a docs correction. The pipeline exists so that each one is checked against
what the project already decided **before** any code is written.

```
/change-context  <slug | "prose">   → docs/changes/<slug>/context.md
/change-validate <slug>             → docs/changes/<slug>/validation.md
/change-plan     <slug>             → docs/changes/<slug>/plan.md
/implement-change <slug>            → code + docs/changes/<slug>/progress.md
/pre-pr          <slug>             → the gate before opening the PR
```

Adjacent, invoked on their own:

- `/decide "<need>"` — triage a need against `docs/DECISIONS.md`; record a new
  decision or point back at the one that already answers it. **The right front
  door when the input is a question rather than a task.**
- `/contract-sync` — find drift between handlers, `docs/openapi.yaml`,
  `README.md`, `CLAUDE.md` and `.env.example`.
- `/go-testing-guide` — what to test at each layer in this repo.

## When to skip stages

The pipeline is proportional to risk, not ceremony. Three tiers:

| Change | Stages |
|---|---|
| Typo, comment, doc wording, a test for existing behaviour | none — just do it, then `/pre-pr` |
| A bug fix or a small addition inside one package, no contract change | `/change-plan` (it bootstraps a minimal context) then implement |
| New endpoint, new config var, schema change, new dependency, anything that touches an invariant in `CLAUDE.md` or a decision in `docs/DECISIONS.md` | the full pipeline |

When unsure which tier applies, run `/change-context` — its first output is the
tier assessment, and it is cheap.

## Directory layout

```
docs/changes/<slug>/
├── context.md      # owned by change-context
├── validation.md   # owned by change-validate
├── plan.md         # owned by change-plan
└── progress.md     # owned by implement-change
```

`<slug>` is kebab-case, ≤40 chars, and matches the branch name after its prefix
— branch `feature/orphan-blob-collector` → slug `orphan-blob-collector`. Every
artifact is owned by exactly one stage. A later stage may patch an earlier
artifact (validate marks issues resolved in place) but never recreates one.

`docs/changes/` is working material, not the project's permanent record. What
outlives the change goes to `docs/DECISIONS.md` (why), `docs/ARCHITECTURE.md`
(design), `docs/openapi.yaml` (contract) and `CHANGELOG.md` (what a client
sees).

## Shared convention — frontmatter

Every artifact starts with:

```yaml
slug: <slug>
stage: context | validation | plan | progress
tier: trivial | contained | full
sources_mtime:
  docs/DECISIONS.md: <ISO-8601>
  docs/openapi.yaml: <ISO-8601>
```

`sources_mtime` records the mtime of every upstream file the artifact was built
from. **Before reading a prior artifact, a stage compares each recorded mtime to
the file's current one.** If any source is newer, abort:

```
context.md está obsoleto em relação a docs/DECISIONS.md
(registrado: X, atual: Y). Rode /change-context <slug> antes de continuar.
```

Never auto-regenerate. Staleness always aborts with the next command.

## Shared convention — abort with a command

When a stage cannot proceed it stops and names exactly what to run:

```
{one sentence on what is missing or wrong}. Rode {comando} {args} primeiro.
```

Never guess, never partially proceed, never invent a decision the project has
not made. A change that appears to contradict `docs/DECISIONS.md` is a
**question for the user**, not a judgement call — this is stated in `CLAUDE.md`
and the pipeline enforces it at `/change-validate`.

## Shared convention — issue IDs

`validation.md` uses stable per-category IDs, sequential within their category
and never reused:

| Category | ID | Meaning |
|---|---|---|
| Decision conflict | `DC-N` | Contradicts a recorded decision in `docs/DECISIONS.md` |
| Invariant conflict | `IV-N` | Contradicts an invariant in `CLAUDE.md` or `.claude/rules/` |
| Contract gap | `CG-N` | Wire behaviour changes with no matching `docs/openapi.yaml` edit planned |
| Parity gap | `PG-N` | Touches one `Repository`/`BlobStore` implementation but not its twin |
| Test gap | `TG-N` | Behaviour with no test at the layer that owns it |
| Doc drift | `DD-N` | README / CLAUDE.md / .env.example / CHANGELOG will fall out of sync |
| Ambiguity | `AM-N` | The request admits two readings that lead to different code |
| Already deferred | `AD-N` | Already recorded in `docs/ARCHITECTURE.md` § Future Improvements |

`DC-N` and `IV-N` are **blocking** — they can only be closed by a user decision,
never by the agent choosing. The rest can be closed by planning the missing work.

## Shared convention — read strategy

The repository's own documents are large: `CLAUDE.md` ~23 KB,
`docs/openapi.yaml` ~1600 lines, `README.md` ~24 KB. Loading them whole is what
makes long sessions degrade. Every stage therefore:

1. **Delegates heavy reads to the readers in `.claude/agents/`** —
   `decisions-reader`, `architecture-reader`, `openapi-reader`,
   `invariants-reader`, `test-surface-reader`. Dispatch them **in parallel**;
   each returns a distilled structure, never raw file content.
2. **Greps for a heading map, then reads a bounded line range.** Never `-A N`
   blindly — locate the target heading and the next sibling heading, then read
   exactly between them.
3. **Reads its own stage artifact in full** (they are small by construction) and
   nothing else from `docs/changes/`.
4. **Aborts in O(1) before any heavy read** — does the prior artifact exist, is
   the slug unambiguous, is `sources_mtime` fresh.

## Shared convention — document language

The repository is deliberately mixed and each document has a fixed language.
Match the file you are writing into:

| Document | Language |
|---|---|
| `CLAUDE.md`, `README.md`, `docs/ARCHITECTURE.md`, `docs/openapi.yaml`, code comments | English |
| `docs/DECISIONS.md`, `CHANGELOG.md`, commit messages, PR descriptions | Portuguese |
| `docs/changes/**` (pipeline artifacts) | Portuguese |
| `.claude/**` (skills, rules, agents) | English |

Identifiers, filenames and command names are always English, in every document.

## Definition of done

A change is done when **all** of these hold — this is what `/pre-pr` checks:

1. `make check` is clean (gofmt, vet ×2, staticcheck ×2, govulncheck, `-race`
   unit tests).
2. `make test-integration` is clean when the change touched a repository, a
   migration or a blob store.
3. `docs/openapi.yaml` matches the handlers.
4. Every document the change moved is updated in the **same** commit —
   `README.md`, `CLAUDE.md`, `docs/ARCHITECTURE.md`, `.env.example`,
   `CHANGELOG.md`.
5. `go mod tidy` leaves `go.mod`/`go.sum` unchanged.
6. Nothing was weakened to make a check pass.
