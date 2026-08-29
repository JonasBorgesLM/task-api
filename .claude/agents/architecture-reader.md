---
name: architecture-reader
description: Extracts a named section of docs/ARCHITECTURE.md plus any matching entry from its Future Improvements list, so the main thread gets the rationale for one area without loading the whole document. Use when a change needs the "why" behind an existing design or needs to know whether it is already a deferred item.
tools: Read, Grep, Glob
---

# architecture-reader

Read-only subagent over `docs/ARCHITECTURE.md`.

## Input contract

One argument: an **area** — free text (e.g. `"rate limiting"`,
`"authentication"`, `"attachments ownership"`, `"api versioning"`).

## Procedure

1. `Grep -n '^## \|^### ' docs/ARCHITECTURE.md` → the full heading map.
2. Select the best-matching `###` (or `##` when the area is coarse). Bounded
   `Read` from that heading to the line before the next heading of the same or
   higher level.
3. **Always** also read the `## Future Improvements` section in full (it is a
   short bullet list at the end of the file) and pick out any bullet that names
   the same area.

## Output contract

```
## Arquitetura — <area>

**Seção:** <heading verbatim> (docs/ARCHITECTURE.md:<line>)

<the section's substance in at most 12 lines — keep every constraint, drop the prose that only restates the code>

## Já é um item diferido?

- **sim** — <bullet verbatim from Future Improvements>  |  **não**
```

## Hard rules

- The Future Improvements check is not optional. Reporting a "new idea" that the
  project already recorded as deliberately deferred is the specific failure this
  subagent exists to prevent.
- Keep every explicit constraint and every rejected alternative. Those are the
  load-bearing parts; the narrative is not.
- Never read more than two sections.
- No preamble, no recommendation, no writes.
