# Gotchas — mistakes this repository has already made or narrowly avoided

## Length limits: runes, not bytes — except for passwords

`len(string)` counts bytes. For any non-ASCII input that is a *stricter* limit
than intended, and it silently over-rejects. `task.Service` uses
`utf8.RuneCountInString` for title and description; the PostgreSQL
`VARCHAR(200)` counts characters too, so they agree.

**The deliberate exception:** `user.validatePassword` measures **bytes**, because
bcrypt itself operates on bytes and rejects input over 72 of them. Byte length is
what actually matters there. Do not "fix" it.

A boundary test that only uses ASCII proves nothing about this.

## The build tag is the only thing separating the suites

A PostgreSQL-dependent test in a file without `//go:build integration` runs in
`go test ./...` and fails for everyone with no database. A test that *should*
run in the unit suite but carries the tag silently never runs at all — which is
worse, because the suite stays green.

CI deliberately does **not** filter integration tests by `-run Postgres`: a
`-run` filter once silently skipped every integration test that did not match the
name pattern.

## Integration tests share one database

They run with `-p 1`. `internal/platform/migrate`'s tests `DROP TABLE` the schema
the other packages' tests assume exists. Running them concurrently is a real,
observed source of flakes — not a theoretical one.

## `sql.Open` does not connect

It validates the DSN and returns. `openDatabase` pings explicitly so a bad
`DATABASE_URL` fails at startup rather than as a mysterious error on the first
request. A test that only calls `sql.Open` has verified nothing about
reachability.

## `LIMIT -1` is an error in PostgreSQL

"No limit" reaches the query as SQL `NULL`, not as a negative number. Test the
`limit < 0` path against the real database, not only the memory store — the two
express the same contract through entirely different mechanisms.

## A malformed UUID behaves differently per backend

`::uuid` rejects it as a query error (surfacing as `500`); the memory store
returns `ErrNotFound` (`404`). `TestPostgres_FindByID_MalformedID` **pins** the
current behaviour deliberately. It is recorded in `docs/ARCHITECTURE.md`
§ Future Improvements. If you fix it, that test changes too — and the fix must be
above both repositories so they agree.

## `http.ServeContent` sets `Content-Length` itself

Setting it beforehand is at best redundant and at worst wrong for a Range
response. The same applies to `Content-Type` when ServeContent would sniff it.

## Recovery can write a header that was already written

If a handler panics *after* writing a response, `Recovery`'s `WriteHeader(500)`
produces a "superfluous WriteHeader call" and appends a second body. A test that
panics before writing anything will never see this.

## `errors.Is`, not `==`

`attachment.Service.Upload` compares `err == ErrTooLarge` and `err != io.EOF`
directly. It works today only because both stores return the sentinel bare. A
test that goes through a wrapping store is what catches the day one of them
stops.

## `moat`'s limiters own goroutines

Each `ratelimit.New` starts a goroutine that expires idle buckets. A test that
builds a server per test case and never calls the close function leaks one per
case. `newServer`'s returned close function exists for exactly this — call it,
including on the error paths.

## Fakes over mocks, always

Every package defines its own `fakeRepository` / `fakeService`. Adding a mocking
library to "save time" on one test costs the project a dependency and a second
idiom forever.

## Concurrency needs goroutines

`TestConcurrentUpdate_LosersGetErrConflict` races real goroutines under `-race`.
Sequential calls that assert the same final state prove nothing about the race
the code exists to lose safely.

## Fuzzing catches what examples cannot

Every example-based blob store test uses well-formed UUID keys — which have
nothing to escape with. `FuzzFSBlobStore_OpenNeverEscapesRoot` is the only thing
standing between a change in key generation and a path traversal.
