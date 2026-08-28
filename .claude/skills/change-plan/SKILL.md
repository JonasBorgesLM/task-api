---
name: change-plan
description: "Stage 3 of the task-api change pipeline. Turns a validated context into docs/changes/<slug>/plan.md — ordered implementation steps, each with the files it touches, the tests it must make pass and the docs it must move. Use after /change-validate reports clean, or directly for a contained change."
disable-model-invocation: true
---

# /change-plan

Stage 3. Emits the plan `/implement-change` executes. The plan is a **contract**:
it makes the technical decisions so that implementation makes none.

## Preflight

1. `context.md` exists and its `sources_mtime` is fresh → else abort to
   `/change-context <slug>`.
2. `validation.md` exists → else abort:
   `"Falta validation.md para <slug>. Rode /change-validate <slug> primeiro."`
3. `validation.md` frontmatter is `status: clean` → else **hard block**:
   ```
   validation.md tem N questões abertas (M bloqueantes). Resolva-as, rode
   /change-validate <slug> de novo, e então volte aqui.
   ```
4. **Contained-tier bootstrap:** if `context.md` is absent and the user asked
   for a contained change directly, build a minimal in-memory context (dispatch
   `invariants-reader` and `test-surface-reader` only) and proceed. Do not skip
   this for a full-tier change.

## Step structure

The unit is a **CI — Change Item**, labelled `CI-1`, `CI-2`, … Each one is
independently verifiable: after it, the tree compiles and `make check` passes.
A step that leaves the tree broken is two steps that were merged by mistake.

Ordering rules, in priority order:

1. **Migration before the code that needs it.** A migration is always its own
   `CI`, paired with its `migrate_test.go` update.
2. **Interface before implementations.** `repository.go` first, then *both*
   `memory_repository.go` and `postgres_repository.go` — in the **same** `CI`,
   never split, so parity cannot drift between steps.
3. **Service before Handler before wiring.** Bottom-up, following the dependency
   direction.
4. **`docs/openapi.yaml` in the same `CI` as the handler it describes.** Not a
   trailing docs step — a trailing docs step is the one that gets dropped.
5. **`CHANGELOG.md` last**, once the client-visible surface has settled.

## Output — `docs/changes/<slug>/plan.md`

```markdown
---
slug: <slug>
stage: plan
tier: contained | full
items: N
sources_mtime:
  docs/changes/<slug>/context.md: <ISO-8601>
  docs/changes/<slug>/validation.md: <ISO-8601>
---

# <slug> — Plano

## Objetivo

<2–4 lines: what is true after this change that is not true now. Written so a
reviewer can check it against the diff.>

## Restrições herdadas

<the blocking invariants and decisions from context/validation, one imperative
line each. This is what the implementer must not break; it is repeated here so
the plan is self-contained during a long execution.>

## Itens

### CI-1 — <short imperative name>

- **Arquivos:** `path:símbolo` — <what changes>, one line per file
- **Faz:** <2–4 lines. Concrete enough to implement without re-deriving the
  design; not a diff.>
- **Não faz:** <the adjacent thing a reasonable implementer would be tempted to
  do here, and which belongs elsewhere — or `_nada_`>
- **Testes:** <exact file, exact test name, exact assertion. Include the build
  tag when it is an integration test.>
- **Verificação:** `make test` | `make check` | `make test-integration` — the
  minimum that proves this item
- **Depende de:** CI-N | _nada_

### CI-2 — ...

## Mapa de dependências

```
CI-1 → CI-2 → CI-4
CI-3 (independente)
```

## Entregáveis

- [ ] <code artifact>
- [ ] <test artifact>
- [ ] <doc artifact>

## Riscos e como o plano os cobre

| Risco | Coberto por |
|---|---|
| <the specific way this could silently break> | <CI-N / a named test> |
```

## Hard rules

- **Every `CI` with a behaviour change has a `Testes` line naming a real file.**
  "Add tests" is not a test plan. If a step genuinely needs no test (a comment
  fix, a doc edit), write `_nenhum — não altera comportamento_` and say why.
- **Never plan a `Repository` change that touches one implementation.** If the
  plan splits memory and postgres into separate items, merge them.
- **Never plan a step that weakens a check** to make another step pass.
- **A new dependency is its own `CI`**, and the plan states which
  `docs/DECISIONS.md` section records the choice. `CLAUDE.md` is explicit that
  dependencies are not added lightly: pure Go, no cgo (the `scratch` build
  depends on it), and prefer `moat`'s zero-dependency packages.
- Write the plan **item by item**. After an item is written into the file, do not
  re-read it; grep its heading if a later item needs to reference it.

## Output to the user

Item count, the dependency map, and:
`"docs/changes/<slug>/plan.md escrito. Próximo: /implement-change <slug>"`
