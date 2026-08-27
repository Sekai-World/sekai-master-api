# Cloud-Native Roadmap

This roadmap records the work needed to move `sekai-master-api` from a
Kubernetes-deployable service toward a more autonomous cloud-native platform.
It is an implementation backlog, not a claim that every item is required for
the API to run in Kubernetes today.

## Current position

The repository already has a solid runtime foundation:

- A Helm chart with separate `serve` and `control` roles.
- Horizontally scalable, stateless public reads in `serve`.
- Optional HPA, rolling updates, resource requests/limits, topology settings,
  and root-level liveness/startup/readiness probes.
- External PostgreSQL and Redis dependencies.
- ConfigMap/Secret-based configuration, non-root containers, dropped
  capabilities, and a read-only root filesystem.
- OpenTelemetry metrics/tracing integration and local/production observability
  documentation.
- Version payloads persisted to Redis by `control` on sync completion
  ([PR #68](https://github.com/Sekai-World/sekai-master-api/pull/68),
   [PR #74](https://github.com/Sekai-World/sekai-master-api/pull/74)).

The main remaining gap is the control plane: synchronization, migrations,
Redis recovery, and lifecycle state are not yet independently coordinated or
fully automated.

## Resolved issues

| Issue | Resolution | Notes |
|-------|-----------|-------|
| [#67](https://github.com/Sekai-World/sekai-master-api/issues/67) — OIDC host-only routing | Fixed by [PR #68](https://github.com/Sekai-World/sekai-master-api/pull/68) | Issuer/audience validation now matches on host only, not full URL path. |
| [#73](https://github.com/Sekai-World/sekai-master-api/issues/73) — VersionByRegion returns empty on serve pod | Fixed by [PR #74](https://github.com/Sekai-World/sekai-master-api/pull/74) | Version payload is now written to Redis by `control`; `serve` reads from Redis. Upgrade-commit shortcut completeness remains tracked by [#76](https://github.com/Sekai-World/sekai-master-api/issues/76). |

## P0 — Version cache completeness and split-deployment verification

Goal: close remaining gaps in the version-persistence story and verify
split-deployment correctness end to end.

### 1. Version payload completeness

- [ ] Verify that all version fields (commit, upgrade, unchanged regions) are
  correctly persisted to Redis by `control` and read back by `serve`.
- [ ] Audit the upgrade-commit shortcut path for correctness under partial-sync
  and interrupted-sync scenarios
  ([#76](https://github.com/Sekai-World/sekai-master-api/issues/76)).
- [ ] Add integration tests covering: full sync → version read, partial sync →
  upgrade commit, and interrupted sync → graceful fallback.
- [ ] Verify `serve` readiness probe returns the correct status when Redis is
  available but version data is missing or stale.

**Acceptance:** a split `control`/`serve` deployment serves correct version
data for all regions under normal, degraded, and recovery conditions.

### 2. Readiness and rollout contract ([#77](https://github.com/Sekai-World/sekai-master-api/issues/77))

- [ ] Add a dedicated version-readiness endpoint (or extend the existing health
  endpoint) that reports per-region version data availability.
- [ ] Wire the `serve` Kubernetes readiness probe to this endpoint so that pods
  are removed from the Service when version data is unavailable.
- [ ] Document the rollout sequence: `control` must complete at least one sync
  before `serve` pods are considered ready.
- [ ] Add deployment smoke tests for public reads, admin access, webhook
  handling, and the admin SSE stream.

**Acceptance:** a fresh deployment cannot route public traffic to an empty or
unready Redis data plane.

### 3. Graceful shutdown ([#79](https://github.com/Sekai-World/sekai-master-api/issues/79))

- [ ] Ensure in-flight sync and version-write operations complete before pod
  termination (proper `SIGTERM` handling with a shutdown grace period).
- [ ] Add a pre-stop hook if the Kubernetes grace period alone is insufficient
  for long-running syncs.

**Acceptance:** a pod termination during sync does not leave Redis in an
inconsistent state.

## P1 — Deployment hardening

Goal: make the current two-role architecture safe to operate repeatedly in a
cluster without relying on undocumented manual sequencing.

### 4. External dependency durability

- [ ] Use managed or highly available PostgreSQL and Redis in production.
- [ ] Define Redis persistence (AOF/RDB), backup, restore, retention, and
  recovery objectives.
- [ ] Run and document a Redis-loss recovery drill using `control` force sync.
- [ ] Monitor Redis/PostgreSQL connectivity, saturation, and error rates.

**Acceptance:** a Redis replacement can be restored or rebuilt through a
documented, repeatable operation, and the resulting data/version state is
verified before public traffic is enabled.

### 5. Separate schema migration from application rollout

- [ ] Run Goose migrations from a dedicated Kubernetes Job or release hook.
- [ ] Keep application startup independent from schema migration completion.
- [ ] Define expand/contract compatibility rules for migrations that span
  multiple application versions.
- [ ] Make migration status and failure diagnostics visible in deployment
  automation.

**Acceptance:** a failed migration blocks the rollout without leaving a
partially available application deployment, and a successful migration can be
verified before the new application image receives traffic.

### 6. Infrastructure policies ([#78](https://github.com/Sekai-World/sekai-master-api/issues/78))

- [ ] Add PodDisruptionBudgets for both `control` and `serve`.
- [ ] Add NetworkPolicies to restrict inter-pod traffic to required paths.
- [ ] Add topology spread constraints to avoid co-locating `control` and
  `serve` pods on the same node.
- [ ] Enable image scanning/signing and automated rollback policy where the
  cluster supports them.
- [ ] Enforce TLS termination at the ingress layer; document certificate
  rotation.

**Acceptance:** infrastructure policies are enforced declaratively and
validated in CI, and the cluster rejects deployments that violate them.

## P2 — Control-plane resilience

Goal: remove the current single-process coordination limit while preserving
single-writer behavior for synchronization.

### 7. Distributed synchronization coordination ([#80](https://github.com/Sekai-World/sekai-master-api/issues/80))

- [ ] Replace process-local active-sync locking with PostgreSQL advisory locks,
  a Redis lease, or an equivalent distributed coordinator.
- [ ] Persist sync leases and ownership metadata.
- [ ] Add fencing tokens so an expired or partitioned worker cannot continue
  writing after another worker takes ownership.
- [ ] Make sync state transitions idempotent and safe across retries.
- [ ] Add leader-election or an equivalent worker model before increasing
  `control` replicas.

**Acceptance:** two `control` pods cannot concurrently own the same sync job;
an owner can fail and another pod can safely resume; stale owners cannot write
after lease loss.

### 8. Horizontally resilient control role

- [ ] Remove the deployment requirement for `control.replicaCount: 1` once
  distributed coordination is proven.
- [ ] Replace the unconditional `Recreate` strategy with a safe rolling
  strategy where appropriate.
- [ ] Test control-pod interruption during migrations, sync, and interrupted
  sync recovery.
- [ ] Add takeover latency and failed-job recovery metrics.

**Acceptance:** a control-pod replacement completes without manual cleanup,
duplicate sync ownership, or inconsistent persisted status.

## P3 — SLO and recovery automation

Goal: turn observability and recovery capabilities into measurable service
objectives and repeatable operations.

### 9. SLOs and alerting ([#81](https://github.com/Sekai-World/sekai-master-api/issues/81))

- [ ] Define availability and latency SLOs for public read endpoints.
- [ ] Define freshness/sync-lag SLOs for each configured region.
- [ ] Alert on sync failures, stuck syncs, Redis/PostgreSQL dependency errors,
  data-version mismatches, readiness flapping, and rollout failures.
- [ ] Add dashboards for control lifecycle state, Redis recovery state, and
  serve readiness by region.
- [ ] Route traces to durable Tempo storage in production; do not leave them
  on a debug exporter.

**Acceptance:** every production alert links to a runbook, and the dashboards
show whether an incident is caused by application health, dependency health,
or master-data freshness.

### 10. Delivery and recovery automation

- [x] Build immutable, versioned images. Delivered by the Phase 1 release
  pipeline (`docs/release.md`, `.github/workflows/release.yml`): a tag-driven
  workflow that publishes semver, `latest`, and `sha-*` tagged images to
  `docker.dnaroma.eu/sekai-world/sekai-master-api` with the pushed digest
  recorded in the release notes and step summary, enabling digest-pinned
  deployments and digest-based rollback. Provenance attestations and
  vulnerability scanning remain open follow-ups.
- [ ] Automate Helm validation, migration checks, deployment smoke tests, and
  rollback tests in CI/CD.
- [ ] Define backup restore drills for PostgreSQL and Redis.
- [ ] Document RTO/RPO targets and verify them periodically.
- [ ] Keep production configuration and infrastructure manifests versioned,
  reviewable, and separated from secret values.

**Acceptance:** a new version can be promoted, verified, rolled back, and
recovered using the delivery pipeline and documented runbooks without
ad-hoc shell changes in the cluster.

## Current architectural constraints to preserve until addressed

- `serve` is the horizontally scalable public-read role.
- `control` owns migrations, synchronization, webhook handling, and persisted
  search-index repair.
- `control` remains a single replica with `Recreate` deployment semantics
  until distributed locking and fencing are implemented (P2).
- `serve` readiness currently checks cards data availability, not version
  data availability; this will be updated as part of P0 item 2.
- Redis is a shared persisted data plane; `serve` must not silently rebuild an
  empty Redis.

## Source references

- `deploy/helm/sekai-master-api/README.md`
- `deploy/helm/sekai-master-api/templates/deployments.yaml`
- `deploy/helm/sekai-master-api/values.yaml`
- `docs/development.md`
- `docs/production-observability-k3s.md`
- `cmd/api/main.go`
- `internal/observability/otel.go`
- `internal/storage/migrate.go`
- `internal/transport/http/middleware/startup_gate.go`
- [Issue #67](https://github.com/Sekai-World/sekai-master-api/issues/67)
- [Issue #73](https://github.com/Sekai-World/sekai-master-api/issues/73)
- [Issue #76](https://github.com/Sekai-World/sekai-master-api/issues/76)
- [Issue #77](https://github.com/Sekai-World/sekai-master-api/issues/77)
- [Issue #78](https://github.com/Sekai-World/sekai-master-api/issues/78)
- [Issue #79](https://github.com/Sekai-World/sekai-master-api/issues/79)
- [Issue #80](https://github.com/Sekai-World/sekai-master-api/issues/80)
- [Issue #81](https://github.com/Sekai-World/sekai-master-api/issues/81)
- [PR #68](https://github.com/Sekai-World/sekai-master-api/pull/68)
- [PR #74](https://github.com/Sekai-World/sekai-master-api/pull/74)
