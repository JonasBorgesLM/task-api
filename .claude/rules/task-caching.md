---
paths:
  - 'internal/task/cached_repository*.go'
  - 'cmd/api/main.go'
description: 'Cache-aside invariants for GET /v1/tasks: what never gets cached, fail-open invalidation, the composite key, the dedicated Redis contract'
---

# Task list caching (`cistern`)

Every rule here is load-bearing. Each one has already been decided in
`docs/DECISIONS.md` § "Cache-aside para GET /v1/tasks" (including its
correction paragraph); a change that contradicts one is a question, not a
task.

## What never gets cached

- `CountAll` and `CountByStatusAndPriority` are pure passthrough to `next`,
  always. `GET /v1/tasks`'s `X-Total-Count` must reflect a real, current
  count on every response (`docs/DECISIONS.md` § "Total real na listagem") —
  a cached total could disagree with the page actually returned.
- `pageETag`'s hash source never changes: `(id, version)` of the rows
  `FindAll` actually returned, whether they came from cache or `next`. The
  cache is never a second source for that hash.

## The cache key

`FindAll`'s full signature — `userID`, `statuses`, `priorities`, `limit`,
`offset` — not just `userID`. `docs/DECISIONS.md` § "Índices para
status/priority em tasks" is why: no index backs those filters, so a key
that ignored them would answer one filter's request with another filter's
page. `cacheKey` (`internal/task/cached_repository.go`) folds
`statuses`/`priorities` into sorted, comma-joined strings — a generic
`cistern.Cache[K, V]`'s key type must satisfy Go's `comparable`, which
slices do not. Invalidation stays coarse on purpose: one tag per user
(`user:<id>:lists`), never per filter combination.

## Invalidation is synchronous, and fails open

`Create`/`Update`/`Delete` invalidate the owner's tag immediately after
`next` succeeds — never before, and never at all if `next` failed. A
publish failure (`Bus`/Redis unreachable) is **never** returned as the
write's own error. It is reported through `OnInvalidationError` (wired to
the logger in `cmd/api`, kept out of `Service`/`Handler`), and the worst
case is a stale L1 entry served until its own TTL. Do not "fix" this by
making an invalidation failure fail the write — that reverts `cistern`'s
deliberate fail-open design and would let a Redis blip start rejecting task
creates/updates.

## Reads fail open too

`buildCachedTaskRepository` (`cmd/api/main.go`) never pings Redis at
startup, unlike `openDatabase`'s `PingContext` against `DATABASE_URL`. An
unreachable `REDIS_ADDR` must surface as a slower `GET /v1/tasks`, never a
refusal to start. `cistern.New` still fails startup for a genuine
configuration mistake (e.g. `REDIS_CACHE_L1_TTL` exceeding
`REDIS_CACHE_TTL`) — that one is real misconfiguration, not a transient
outage.

## The Redis instance

- `maxmemory-policy allkeys-lru`, never `noeviction` — `noeviction` is
  `cairn.Store`'s contract, for durable data, not a cache. Losing an entry
  under memory pressure must cost a miss, never a rejected write.
- Authenticates as the dedicated ACL user `cistern`
  (`RedisUsername`/`RedisPassword`), restricted to `cistern:*` keys and the
  `cistern:v1:bus` Pub/Sub channel — the exact rule
  `redisstore/README.md` documents.
- **Never shared** with a future distributed rate limiter or with
  `cairn.Store`'s `redisstore` — `cistern`'s own documentation rules this
  out (`maxmemory-policy` applies to the whole instance, not per logical
  database), not as an option to weigh. Either future use needs its own,
  separate instance.

## Layering

`CachedRepository` implements `task.Repository` whole — the same decorator
shape `breakerRepository` (`postgres_breaker.go`) uses. `task.Service` and
`task.Handler` must stay completely unaware that Redis or `cistern` exist.
`cmd/api/newServer` is the only place that decides whether the cache is in
the stack at all (`cfg.RedisAddr == ""` ⇒ `taskRepo` unwrapped).

## Resource cleanup

`buildCachedTaskRepository` returns a close function that closes both the
`*task.CachedRepository` (its `Bus` subscription, via `CachedRepository.
Close`) and the `*redis.Client` it built — composed into `newServer`'s
`closeAll`, the same shape `buildBlobStore`'s `closeStore` already uses. A
change that adds a new resource here (another client, another goroutine)
must extend this closure, not leave it to the process exiting.
