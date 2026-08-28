---
paths:
  - 'docs/openapi.yaml'
  - 'internal/*/handler.go'
description: 'openapi.yaml is the source of truth for the wire contract and changes in the same commit as the handler'
---

# API contract

`docs/openapi.yaml` is the source of truth for every endpoint, request/response
schema, status code and example. It describes **what the code does**, not what
it should do — a known gap is documented as-is, with the intended fix recorded
in `docs/ARCHITECTURE.md` § Future Improvements.

## Change together, always

Touching a handler's observable behaviour — a new field, a new status code, a
new query parameter, a changed error message shape — updates
`docs/openapi.yaml` **in the same change**. There is no follow-up commit for
this.

Then check whether the same change also moves:

- `README.md`'s endpoint table (routes, auth requirement, one-line purpose).
- `CLAUDE.md`, if it changed an invariant that file states.
- `docs/DECISIONS.md`, if the change reverses or qualifies a recorded decision.
- `CHANGELOG.md`, if it is visible to a client — and mark it **BREAKING** when
  an existing caller stops working.

## Path conventions

- Contract paths are documented **with** the `/v1` prefix (`/v1/tasks/{id}`).
- Operational paths are documented **without** one (`/health`, `/health/ready`,
  `/debug/vars`) — they are deliberately unversioned.
- A download addresses a blob by its own key (`/v1/files/{key}`) rather than
  being nested under the task, so the client cannot assert a task/blob pairing
  the server would then have to believe.

## Status codes this API has already settled

| Condition | Status |
|---|---|
| Unknown id, or a row owned by another user | `404` |
| Validation failure (input, priority, status name, content type, size) | `400` |
| Duplicate registration | `409` |
| Optimistic-concurrency loss | `409` |
| Illegal status transition | `409` |
| Missing/expired/malformed bearer token | `401` |
| Session lookup itself failed (database down) | `503` — never `401` |
| Rate limit | `429` |

Do not invent a new code for a condition already in this table.

## Versioning

A breaking change to the contract is a **new mount** (`/v2`), never an edit to
`/v1`'s meaning. Never add an unversioned alias or a redirect for a contract
path — that makes the prefix decorative and breaks the callers versioning
exists to protect.
