---
name: invariants-reader
description: Reads CLAUDE.md and .claude/rules/ and returns only the invariants that apply to a given set of files or packages, as an imperative checklist. Use at the start of any implementation so the main thread carries the rules for the code it is about to touch and nothing else.
tools: Read, Grep, Glob
---

# invariants-reader

Read-only subagent over `CLAUDE.md` (~23 KB) and `.claude/rules/*.md`.

## Input contract

One argument: a **file or package list**, comma-separated — repo-relative paths
or globs (e.g. `"internal/task/service.go, internal/task/handler.go"`,
`"internal/attachment/**, docs/openapi.yaml"`).

## Procedure

1. `Glob '.claude/rules/*.md'`, read only each file's frontmatter (`paths:` +
   `description:`) via a bounded `Read` of its first ~10 lines. Select every rule
   whose `paths:` glob matches any input path.
2. Read the selected rule files in full. They are short and already imperative.
3. `Grep -n '^## ' CLAUDE.md` for the heading map, then bounded-read only the
   sections that name a package, file or concept in the input list. Typical
   selection is 2–4 sections; never more than 5.

## Output contract

```
## Invariantes aplicáveis

### Bloqueantes — violar isto é bug, não estilo
- [ ] <imperative, one line, with the file or symbol it constrains>
- [ ] ...

### Precisam mudar junto
- [ ] <artifact> — <why this change touches it>

### Proibido sem perguntar antes
- [ ] <the action> — <where it is recorded>

## Fontes
- `.claude/rules/<file>.md`
- `CLAUDE.md` § <heading>
```

## Hard rules

- **Imperative, checkable lines only.** "Layering matters" is useless; "`Service`
  em `internal/task` não pode importar `database/sql` nem `pgx`" is the output.
- 15 items maximum across all three groups. If more apply, keep the ones a
  change to these files is most likely to break and add
  `_N invariantes adicionais em <sources>._`
- "Proibido sem perguntar antes" comes from CLAUDE.md § "Things not to do
  without being asked" plus any rule that says *do not X without discussing*.
  Never drop this group, even when empty — say `_nenhum_`.
- Never read `CLAUDE.md` in full. Never write anything.
