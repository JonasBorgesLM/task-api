---
paths:
  - '**/*_test.go'
description: 'Fakes not mocks, build tags separate unit from integration, one contract test per interface with two backends'
---

# Tests

## Fakes, never a mocking framework

Every package defines its own `fakeRepository` / `fakeService` in its
`*_test.go`. Do not introduce gomock, testify/mock or any other mocking
library. Table-driven tests with the standard library and `t.Helper()` helpers
are the whole toolkit.

## Unit vs. integration is a build tag, never a convention

- `//go:build integration` at the top of the file is what excludes it from
  `go test ./...`. Any new PostgreSQL- or MinIO-dependent test goes in a file
  carrying that tag.
- `internal/task/integration_test.go` has **no** tag and is a different thing: a
  full-stack HTTP test against the real `memoryRepository`, zero external
  dependencies. Do not blur the two, and do not rename either in a way that
  makes them look alike.
- Integration tests run with `-p 1` (they share one database and one schema;
  `internal/platform/migrate`'s tests `DROP TABLE` what the others assume).

## Naming

Integration tests that need PostgreSQL are named `TestPostgres_*`. This is a
readability convention, not a selector — CI filters by build tag, never by
`-run`, precisely so a mis-named test cannot silently stop running.

## Contract tests for multi-backend interfaces

`BlobStore` has two implementations and **one** set of assertions:
`runBlobStoreContract` in `internal/attachment/storage_contract_test.go`,
deliberately untagged so both suites compile it. A new promise on the interface
goes there, not in one backend's file — an fs-only assertion is one S3 can fail
silently in production. Only genuinely backend-specific properties (path
traversal containment, which belongs to `pathguard`) stay in their own file.

Apply the same shape to any future interface with more than one implementation,
`Repository` included.

## Fuzzing

`FuzzFSBlobStore_OpenNeverEscapesRoot` runs in CI. Path containment is the one
property here where a regression is both silent and severe — example-based
tests all use well-formed UUID keys that have nothing to escape with. If you
change how storage keys are generated or how blobs are read, that target is
what catches you.

## Migration tests

A migration that adds a table also updates
`internal/platform/migrate/migrate_test.go`:
`TestPostgres_RunMigrationsDown_RevertsMostRecentMigration` steps back from the
newest migration **by name** and needs a step for yours, and the `tableExists`
lists name the tables expected to exist. `assertNewestMigrationIs` fails with
that instruction rather than letting the mismatch surface as unrelated
assertion noise.

## Before you call a change done

`make check` (gofmt + vet ×2 + staticcheck ×2 + govulncheck + race-tested unit
tests) must be clean, and `make test-integration` when the change touches a
repository, a migration or a blob store. Never weaken a check to make something
pass.
