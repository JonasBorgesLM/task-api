---
paths:
  - 'internal/*/handler.go'
  - 'internal/*/middleware.go'
  - 'internal/middleware/*.go'
  - 'cmd/api/*.go'
description: 'HTTP layer conventions: ServeMux only, /v1 mount, bounded bodies, JSON envelope, middleware order'
---

# HTTP layer

## Router and versioning

- `net/http`'s `ServeMux` and method patterns (`"POST /tasks"`). **No router
  library, no web framework.**
- Handlers register **unprefixed** patterns. `cmd/api/newServer` mounts them
  with `http.StripPrefix("/v1", v1)`, so a v2 is a second mount rather than an
  edit to every `RegisterRoutes`.
- `/health`, `/health/ready` and `/debug/vars` stay **unversioned** — probes
  and scrapers are operations, not clients. Never add an unversioned alias or a
  redirect for a contract path.
- `/health` is liveness, `/health/ready` is readiness. Do not swap them, and
  keep both outside the global rate limiter: a probe answered `429` reads to an
  orchestrator as an unhealthy replica.

## Request handling

- Every JSON body goes through `http.MaxBytesReader` before `json.Decoder`.
- Every response is written by `writeJSON` / `writeError`; errors always use the
  `{"error": "message"}` envelope, including from middleware
  (`user.writeAuthError`) so the shape never depends on where the request died.
- A list endpoint serialises an empty result as `[]`, never `null`.
- Pagination (`limit`/`offset`) and ownership filtering are pushed into
  `Repository.FindAll` and into the SQL query. Never reintroduce "fetch
  everything, slice in Go" at `Service` or `Handler`.
- `userID` comes from `middleware.UserIDFromContext` and nowhere else — never
  from a body field, a query parameter or a path segment.

## Middleware order (`cmd/api/newServer`)

```
RequestID → Logging → secureheaders → CORS → Recovery → [globalLimiter] → mux
                                                          (outer mux catch-all)
```

Each position is load-bearing:

- `RequestID` first, so `Logging` and `Recovery` can read the ID.
- `Logging` outside `Recovery`, so a recovered panic still logs its real status.
- `secureheaders` outside `CORS`, so headers reach every response that never
  touches the mux: preflight, `429`, `Recovery`'s `500`, the router's `404`/`405`.
- `CORS` outside `Recovery`, so `Access-Control-Allow-Origin` survives a panic.
- The global limiter is mounted on the outer mux's catch-all, not in this chain,
  so the health routes bypass it.
- `userLimiter` stays **inside** `requireAuth` — its key is the user ID
  `RequireAuth` puts in the context, so moving it out leaves it with no key.

## Headers

- CSP stays `default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`.
  Do not fall back to `secureheaders.DefaultCSP` (`'self'`) — it is strictly looser.
- HSTS is sent unconditionally, never with `includeSubDomains` or `preload`.
  `HSTS_MAX_AGE=0` is the opt-out and omits the header entirely.
- Never key a rate limiter on `X-Forwarded-For`/`X-Real-IP` without
  `TRUSTED_PROXIES`; the client writes those headers.
