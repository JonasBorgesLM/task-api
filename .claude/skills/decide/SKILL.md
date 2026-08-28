---
name: decide
description: "Triage a technical need against docs/DECISIONS.md and either point at the decision that already answers it, record a new one, or supersede an existing one with the trade-off stated. Use when the input is a question rather than a task — 'tem decisão sobre X?', 'devo usar Y ou Z?', 'quero mudar como funciona W' — and before any change that reverses a recorded decision."
disable-model-invocation: true
---

# /decide

`docs/DECISIONS.md` is what stops this project re-litigating settled questions
and re-making mistakes it already argued its way out of. This skill is the only
thing that writes to it.

`CLAUDE.md` states the rule this enforces: *if an issue appears to contradict a
recorded decision, stop and ask before proceeding.* `/decide` is where that
question gets asked and answered.

## Input

`/decide "<need in one or two sentences>"`

## Step 1 — triage

Dispatch `decisions-reader` with the need as its topic. Its `## Veredito` block
drives everything after this. Four outcomes:

| Outcome | What it means | What happens |
|---|---|---|
| **Answered** | A recorded decision already covers this | Report it and stop. No write. |
| **Revision** | The decision covers it, but a detail is missing or now imprecise | Add a clarifying paragraph to the existing section. |
| **Supersede** | The need genuinely contradicts a recorded decision | Requires an explicit user decision. Never taken by the agent alone. |
| **New** | No recorded decision covers this | Record a new section. |

State the outcome to the user **before** writing anything.

## Step 2 — gather what a decision needs

A decision is not a preference. Before writing, you must be able to state:

- **The alternative that was considered and rejected**, and why. A section with
  no rejected alternative is a description, not a decision — the existing
  sections all name one.
- **The trade-off accepted.** Every real decision costs something. Naming the
  cost is what lets a future reader tell whether the cost has changed.
- **The blast radius**: which files, which invariants in `CLAUDE.md`, which parts
  of `docs/ARCHITECTURE.md` and `docs/openapi.yaml` this constrains.

If any of the three is missing, ask via `AskUserQuestion` rather than inventing
it. Where the choice is genuinely the user's (a security/UX trade-off, a
dependency, a deployment shape), present the options with their consequences and
let them choose.

## Step 3 — write

`docs/DECISIONS.md`, **in Portuguese**, matching the existing shape exactly:

```markdown
---

## <Tema>: <a escolha, em uma linha>

<2–5 lines stating what was decided, concretely, naming the symbols and files it
governs.>

**Por quê:** <the reasoning, and explicitly the alternative that was rejected
and what is wrong with it.>

**Trade-off aceito:** <the cost, plainly. Never "nenhum".>
```

Placement: append at the end, before nothing. Existing sections are never
reordered — the file reads chronologically, and that ordering is information.

**Supersede** does not delete the old section. Add a line to it:

```
> **Superada em <data>** pela seção "<novo título>" abaixo. Mantida aqui porque
> o raciocínio original continua explicando por que a alternativa foi rejeitada
> na época.
```

Then write the new section, whose `**Por quê:**` states what changed since.

## Step 4 — propagate

A decision that is only in `docs/DECISIONS.md` is a decision nobody will follow.
In the same change:

- [ ] `CLAUDE.md` — if it creates or removes an invariant, or belongs under
      "Things not to do without being asked"
- [ ] `docs/ARCHITECTURE.md` — if it changes a design section, or closes /
      adds a Future Improvements bullet
- [ ] `.claude/rules/*.md` — if it constrains a category of file. Adding the
      rule is what makes the decision fire automatically on the next edit.
- [ ] `docs/openapi.yaml`, `README.md`, `.env.example`, `CHANGELOG.md` — as the
      decision reaches them

Say which of these you updated and which are `n/a`. An unchecked box with no
`n/a` is unfinished work.

## Hard rules

- **Never record a decision the user did not make.** Consolidating a rationale
  the user gave is this skill's job; choosing on their behalf is not.
- **Never write "Trade-off aceito: nenhum".** If nothing was given up, this was
  not a decision worth a section — it belongs in a doc comment.
- **Never delete or rewrite an existing section.** Supersede, annotate, never
  erase. The rejected alternatives are the value.
- **Portuguese**, matching the file. English identifiers, filenames and command
  names, as everywhere else in this repo.
