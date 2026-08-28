---
paths:
  - 'k8s/**'
description: 'Disposable validation cluster: probe roles, drain budget arithmetic, and what these manifests are not'
---

# Kubernetes manifests

`k8s/` is a **disposable validation cluster**, not a production deployment.
PostgreSQL and MinIO run in-cluster on `emptyDir`; the `Secret` is base64, not
encryption. Do not treat `10-postgres.yaml` / `20-minio.yaml` as a template for
anything real, and do not delete the warning comment on the Secret.

## Probes

- `/health` is **liveness** — it answers `200` while the process is alive and
  checks no dependency.
- `/health/ready` is **readiness** — it pings the database.
- Never swap them. Pointing liveness at the readiness route means a database
  blip restarts every pod, turning a recoverable outage into a crash loop.
- Both stay outside the global rate limiter (enforced in `cmd/api/newServer`).

## The shutdown budget is arithmetic, and it is easy to break silently

```
terminationGracePeriodSeconds  >  HTTP_PRE_SHUTDOWN_DELAY
                                + HTTP_SHUTDOWN_TIMEOUT
                                + headroom for the request in flight
```

Currently `30 > 5 + 10`. Raising either timeout without raising the grace period
reintroduces dropped requests during a rollout — **silently**. If you change one
of the three numbers, change the comment that states the arithmetic too.

`maxUnavailable: 0` + `maxSurge: 1` are a pair: that is what makes the rollout
zero-downtime rather than merely fast.

## Startup

`cmd/api` runs migrations **before** it starts listening (`DB_AUTO_MIGRATE`), so
during a slow migration no probe can connect at all — connections are refused,
not answered with a failing status. `startupProbe` (`/health`, `150 ×
2s` = 5 minutes) owns that window; `readinessProbe`/`livenessProbe` only start
running once it has succeeded, and their own `initialDelaySeconds` counts from
that moment, not from container start. Don't shrink the migration budget by
folding it back into liveness's `initialDelaySeconds` — that reintroduces the
mid-migration kill this probe exists to prevent.

**`DB_AUTO_MIGRATE=false` plus a separate `cmd/migrate` Job was considered and
rejected** — see `docs/DECISIONS.md` § "`startupProbe` cobre a migration
lenta". This repo's `k8s/` has no release tool (no Helm, no ArgoCD) to
guarantee a Job completes before the Deployment it would gate; don't introduce
one without discussing it first.

## Container constraints

- `readOnlyRootFilesystem: true` — the filesystem attachment backend
  (`ATTACHMENT_STORAGE_DIR`) cannot work here without a writable volume. These
  manifests use the S3 backend for that reason.
- The image is `FROM scratch` with `USER 65532:65532`. It carries a CA bundle
  (`/etc/ssl/certs/ca-certificates.crt`), so TLS to an external endpoint
  (`ATTACHMENT_S3_USE_SSL=true`, `sslmode=verify-full`) verifies correctly —
  see `docs/DECISIONS.md` § "Bundle de CA na imagem scratch".

## Prove it, don't read it

`k8s/rollout-test.sh` measures the rollout with load running inside the cluster.
If you change the shutdown path, the probes or the rollout strategy, run it — it
caught a real bug that reading the manifests did not.
