---
paths:
  - 'internal/config/*.go'
  - '.env.example'
  - 'docker-compose.yml'
  - 'Dockerfile'
description: 'Every setting is an env var with a documented default, parsed and validated at startup, stdlib only'
---

# Configuration

## Shape

- `internal/config` imports **nothing outside the standard library**. That is
  why `defaultHSTSMaxAge` is duplicated rather than imported from `moat`, and
  why S3 settings are flat fields that `cmd/api` assembles into
  `attachment.S3Config`.
- Every setting is read from an environment variable in `config.Load`, with a
  named `default*` constant and a doc comment on the `Config` field explaining
  what the value means and why the default is what it is.
- Parsing goes through the existing helpers (`parseDuration`,
  `parseNonNegativeDuration`, `parsePositiveInt`, `parsePositiveInt64`,
  `parsePositiveFloat`, `parseBool`, `parseCIDRList`, `parseLogLevel`,
  `parseCommaSeparated`). Add a helper rather than an ad-hoc `strconv` call.

## Fail at startup, never at request time

A bad value returns an error from `Load` and the process exits. Two rules
follow:

- **Never silently fall back to a default** when an explicit value is invalid.
  A malformed `TRUSTED_PROXIES` is rejected at startup precisely because
  degrading to per-proxy buckets would look like working configuration while
  enforcing something else entirely.
- **Zero is sometimes meaningful.** `HSTS_MAX_AGE=0` (opt out of the header) and
  `HTTP_PRE_SHUTDOWN_DELAY=0` (do not wait) use `parseNonNegativeDuration`;
  everything else uses `parseDuration`, which rejects zero.
- Mutually exclusive settings are rejected, not resolved silently —
  `ATTACHMENT_STORAGE_DIR` and `ATTACHMENT_S3_ENDPOINT` together are an error.

## Keep four places in sync

A new or renamed variable changes all of these in the same commit:

1. `internal/config/config.go` — field, default, parse, doc comment.
2. `internal/config/config_test.go` — default, valid value, invalid value.
3. `.env.example` — commented entry with the default and the reasoning.
4. `README.md`'s Configuration table.

And, when it affects a deployment: `docker-compose.yml`, `k8s/30-config.yaml`.

## Secrets

- `.env` is real local config and is gitignored. Only `.env.example`, with
  placeholder values, is tracked.
- Nothing ever logs a whole `Config`, and `AttachmentS3SecretKey` is never
  logged anywhere. Wrapping an error from a client built with credentials must
  not carry them into the message.

## Runtime image constraints

The runtime image is `FROM scratch`: a static binary, no shell, no libc, **no CA
bundle**. Any setting that implies outbound TLS verification —
`DATABASE_URL` with `sslmode=verify-full`, `ATTACHMENT_S3_USE_SSL=true` against
a real endpoint — cannot work in that image without a CA bundle copied in.
Document the constraint next to the setting, or add the bundle; do not let a
setting exist that the shipped image silently cannot honour.
