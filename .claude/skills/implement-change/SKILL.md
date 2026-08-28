---
name: implement-change
description: "Stage 4 of the task-api change pipeline. Executes docs/changes/<slug>/plan.md item by item, running the verification each item names and only advancing when it passes, tracking state in progress.md so the work resumes across sessions. Use for 'implement <slug>', 'execute o plano', 'continue a mudança'."
disable-model-invocation: true
---

# /implement-change

Stage 4. Executes the plan. This skill **does not make technical decisions** —
the plan already made them. A decision that turns out to be missing is a stop,
not an improvisation.

## Input

`/implement-change <slug> [continuous]`

Default mode pauses after each `CI` for confirmation. `continuous` ("execute
tudo", "não pare entre os itens", "autopilot") runs straight through, still
stopping on any failed verification.

## Preflight — before touching code

Stop and surface anything here rather than guessing.

1. **Branch.** `git branch --show-current`. If it is `main` or `develop`, stop
   and ask the user to create `feature/<slug>` (or `fix/`, `docs/`, `ci/` — the
   repo's existing prefixes). The change pipeline never commits to a long-lived
   branch.
2. **Clean tree.** `git status --short`. Uncommitted changes unrelated to this
   slug → stop and ask.
3. **Plan sanity.** `docs/changes/<slug>/plan.md` exists and has `## Itens`, a
   dependency map and `## Entregáveis`. Malformed → stop and report.
4. **Toolchain.** `go version` matches the version pinned in `go.mod`. A
   mismatch is worth naming up front — this repo pins a patch version on
   purpose, and `govulncheck` findings depend on it.
5. **Resume.** If `progress.md` exists, read it and report:
   `"progress.md encontrado: X/Y itens completos. Retomando em CI-Z."`
   Inconsistent with the plan → stop and report.
6. **Services.** If any item's verification is `make test-integration`, check
   PostgreSQL and MinIO are up (`make db-up storage-up`) **before** starting, not
   at the item that needs them.

## Reading the plan

The plan is small enough to read once, in full, at preflight. Read the
`## Restrições herdadas` section carefully — it is the list of things that must
still be true at the end, and it is easy to lose over a long execution.

Do **not** re-read `context.md` or `validation.md`. If an item needs a detail
they hold, grep for it.

## Per-item loop

For each `CI-N`, in dependency order:

1. **Announce.** `CI-N — <name>`, the files it touches, the verification it must
   pass.
2. **Implement.** Only what the item's `Faz` describes. The `Não faz` line is
   binding: an adjacent improvement is an observation for `progress.md`, not an
   edit.
3. **Match the surrounding code.** This repository has a strong, consistent
   voice — copy it rather than your defaults:
   - Every exported type and function gets a doc comment, and it records a
     non-obvious constraint or a rejected alternative. Comments explain **why**,
     never what. A comment that restates the code is noise here.
   - Fakes, not mocks. Table-driven tests. `t.Helper()` on helpers.
   - Errors are sentinels checked with `errors.Is`, wrapped with a short
     operation prefix.
   - No speculative abstraction: no config option, interface or layer for a need
     that does not exist yet.
4. **Verify.** Run exactly what the item names. Never declare an item done on a
   subset.
5. **On failure:** fix the cause. Never `-short`, never a skip, never a disabled
   lint rule, never `--no-verify`. Three failed attempts on the same cause →
   stop, report what you tried and what the failure actually says, and ask.
6. **Record.** Append the item to `progress.md` (see below) before moving on.
7. **Pause** unless in continuous mode.

## `docs/changes/<slug>/progress.md`

```markdown
---
slug: <slug>
stage: progress
status: in_progress | completed
---

# <slug> — Progresso

**Itens:** X/Y concluídos

### CI-1 — <name>
- **Status:** concluído | pendente
- **Verificação:** `make check` — ok | <the failure, verbatim>
- **Arquivos:** `path`, `path`
- **Observações:**
  - <one bullet per note: a deviation from the plan and why, an out-of-scope
    thing noticed, a decision the plan left open and how it was resolved>
  - _nenhuma_ when there is nothing
```

An observation is written the moment it is noticed, not reconstructed at the
end.

## Deviating from the plan

Allowed, and must be recorded, when the plan is **syntactically** wrong — an API
that does not have the shape the plan assumed, a helper that already exists under
another name. Record it in `Observações` with the reason.

**Not allowed** without stopping to ask: changing what the plan decided —
a different error mapping, a different layer, a different dependency, skipping a
test the plan named. Those are `/change-validate` questions, and reaching them
mid-implementation means the plan was incomplete.

## Finishing

When every item is `concluído`:

1. `make check` from a clean state — the whole gate, not the last item's subset.
2. `make test-integration` if any item touched a repository, migration or blob
   store.
3. Tick every box in the plan's `## Entregáveis`; an unticked box is unfinished
   work, not an oversight to mention later.
4. Set `status: completed` in `progress.md`.
5. Report: items completed, files changed, test results **as they actually
   came out**, and every observation that survived as a follow-up.
6. Point at `/pre-pr <slug>`.

Never report a change as done while a check is failing or a deliverable is
unticked. If part of the plan turned out to be blocked, finish everything else
in full and say explicitly what was left and why.
