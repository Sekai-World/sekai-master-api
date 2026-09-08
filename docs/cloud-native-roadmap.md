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
- `serve` readiness (`/readyz`) verifies persisted card records and complete
  version metadata for every configured region, and the Kubernetes readiness
  probe targets that endpoint
  ([PR #86](https://github.com/Sekai-World/sekai-master-api/pull/86)).
- Graceful `SIGTERM`/`SIGINT` shutdown with a bounded grace period, lifecycle
  cancellation, admission gates, worker draining, and ordered dependency
  teardown ([PR #87](https://github.com/Sekai-World/sekai-master-api/pull/87)).
- A `batch/v1` migration Job hook that runs the embedded Goose migrations
  before each install/upgrade and blocks the release on failure, with
  expand/contract compatibility guidance in the chart README.
- Redis durability objectives (RPO ≤ 60 s / RTO ≤ 30 min), a documented
  restore/recovery sequence, and a Redis-loss recovery drill script
  (`scripts/redis-recovery-drill.sh`).
- A deployment smoke script covering the `/readyz` readiness surface, a
  representative public read, admin authentication, webhook rejection, and the
  admin SSE stream (`scripts/smoke.sh`).
- Opt-in PodDisruptionBudgets, NetworkPolicies, and topology spread
  constraints in the chart.

The main remaining gap is still the control plane: distributed synchronization
coordination and fencing ([#80](https://github.com/Sekai-World/sekai-master-api/issues/80))
are not implemented, so `control` stays a single replica. Secondary gaps are
enforcing production-safe deployment defaults ([#78](https://github.com/Sekai-World/sekai-master-api/issues/78))
and measurable SLOs, alerting, and durable trace storage
([#81](https://github.com/Sekai-World/sekai-master-api/issues/81)).

## Resolved issues

| Issue | Resolution | Notes |
|-------|-----------|-------|
| [#67](https://github.com/Sekai-World/sekai-master-api/issues/67) — OIDC host-only routing | Fixed by [PR #68](https://github.com/Sekai-World/sekai-master-api/pull/68) | Issuer/audience validation now matches on host only, not full URL path. |
| [#73](https://github.com/Sekai-World/sekai-master-api/issues/73) — VersionByRegion returns empty on serve pod | Fixed by [PR #74](https://github.com/Sekai-World/sekai-master-api/pull/74) | Version payload is now written to Redis by `control`; `serve` reads from Redis. Upgrade-commit shortcut completeness was tracked by [#76](https://github.com/Sekai-World/sekai-master-api/issues/76). |
| [#76](https://github.com/Sekai-World/sekai-master-api/issues/76) — Commit-unchanged sync path must restore missing Redis versions payload | Fixed by [PR #82](https://github.com/Sekai-World/sekai-master-api/pull/82) | The unchanged-commit shortcut now restores/republishes the version cache; covered by `TestSyncAllVersionCacheShortcutScenarios` and the related skip/restore tests. |
| [#77](https://github.com/Sekai-World/sekai-master-api/issues/77) — serve readiness must verify the versions data contract | Closed by [PR #86](https://github.com/Sekai-World/sekai-master-api/pull/86) | `/readyz` requires persisted records and version metadata per region; probe wired to `/readyz`; `scripts/smoke.sh` verifies the readiness surface. |
| [#79](https://github.com/Sekai-World/sekai-master-api/issues/79) — graceful SIGTERM shutdown | Closed by [PR #87](https://github.com/Sekai-World/sekai-master-api/pull/87) | See P0.3 below. |

## P0 — Version cache completeness and split-deployment verification (complete)

Goal: close remaining gaps in the version-persistence story and verify
split-deployment correctness end to end.

### 1. Version payload completeness

- [x] Verify that all version fields (commit, upgrade, unchanged regions) are
  correctly persisted to Redis by `control` and read back by `serve`
  (`TestStoreAndLoadRegionVersionPayload`,
  `TestStoreAndLoadVersionPayloadCrossInstance`,
  `TestSyncSuccessPersistsVersionPayloadToCache`).
- [x] Audit the upgrade-commit shortcut path for correctness under partial-sync
  and interrupted-sync scenarios
  ([#76](https://github.com/Sekai-World/sekai-master-api/issues/76), fixed by
  [PR #82](https://github.com/Sekai-World/sekai-master-api/pull/82)).
- [x] Add tests covering: full sync → version read, partial sync → upgrade
  commit, and interrupted sync → graceful fallback
  (`TestSyncAllVersionCacheShortcutScenarios`,
  `TestSyncSkipPopulatesVersionCacheWhenManifestUnchanged`,
  `TestSyncSkipFallsBackToFullSyncWhenVersionsPayloadMissing`,
  `TestRecoverInterruptedSyncOnlyRetriesInterruptedRegions`).
- [x] Verify `serve` readiness probe returns the correct status when Redis is
  available but version data is missing or stale
  (`TestReadyReturns503WhenRegionVersionMissing`,
  `TestReadyReturns503WhenRedisErrorLoadingVersion`,
  `TestReadyReturns503WhenVersionCorruptReportedAsMasterData`,
  `TestReadyReturnsOKWhenRecordsAndVersionPresent`).

**Acceptance:** a split `control`/`serve` deployment serves correct version
data for all regions under normal, degraded, and recovery conditions. Met.

### 2. Readiness and rollout contract ([#77](https://github.com/Sekai-World/sekai-master-api/issues/77))

- [x] Add a version-readiness surface that reports per-region version data
  availability (`/readyz` now requires records plus version metadata per
  region, [PR #86](https://github.com/Sekai-World/sekai-master-api/pull/86)).
- [x] Wire the `serve` Kubernetes readiness probe to this endpoint
  (`readinessProbe` targets `/readyz` in
  `deploy/helm/sekai-master-api/templates/_helpers.tpl`).
- [x] Document the rollout sequence in the chart README ("Data and rollout
  behavior"): the migration Job completes first, `control` sync (or force
  sync) restores the Redis data plane, and `serve` pods only pass `/readyz`
  once version data is available.
- [x] Add deployment smoke tests for public reads, admin access, webhook
  handling, and the admin SSE stream (`scripts/smoke.sh`, including
  `SMOKE_CHECK_PROTECTED=true` protected-surface checks).

**Acceptance:** a fresh deployment cannot route public traffic to an empty or
unready Redis data plane. Met.

### 3. Graceful shutdown ([#79](https://github.com/Sekai-World/sekai-master-api/issues/79))

- [x] Ensure in-flight sync and version-write operations complete before pod
  termination (proper `SIGTERM`/`SIGINT` handling with a bounded shutdown grace
  period, lifecycle cancellation, admission gates, worker draining, and ordered
  dependency teardown; delivered by
  [PR #87](https://github.com/Sekai-World/sekai-master-api/pull/87), see
  `cmd/api/main.go`, `cmd/api/shutdown.go`, and the related shutdown regression
  tests).
- Pre-stop hook: evaluated as not needed today — the shutdown path drains
  workers within the configured grace period. Revisit only if production sync
  durations approach the Kubernetes grace period.

**Acceptance:** a pod termination during sync does not leave Redis in an
inconsistent state. Met.

## P1 — Deployment hardening

Goal: make the current two-role architecture safe to operate repeatedly in a
cluster without relying on undocumented manual sequencing.

### 4. External dependency durability (repository-side complete)

- [ ] Use managed or highly available PostgreSQL and Redis in production.
  Documented as a production requirement in the chart README; actual
  provisioning is operator-side and not verifiable from this repository.
- [x] Define Redis persistence (AOF/RDB), backup, restore, retention, and
  recovery objectives (chart README: RPO ≤ 60 s / RTO ≤ 30 min,
  `appendfsync everysec`, off-node encrypted backups, 30-day retention,
  quarterly restore tests).
- [x] Run and document a Redis-loss recovery drill using `control` force sync
  (`scripts/redis-recovery-drill.sh`, chart README recovery sequence,
  `docs/development.md`).
- [x] Monitor Redis/PostgreSQL connectivity, saturation, and error rates
  (application Redis usage metrics plus alerting guidance in the chart README;
  managed-service metrics are scraped provider-side).

**Acceptance:** a Redis replacement can be restored or rebuilt through a
documented, repeatable operation, and the resulting data/version state is
verified before public traffic is enabled. Met on the repository side.

### 5. Separate schema migration from application rollout (complete)

- [x] Run Goose migrations from a dedicated Kubernetes Job or release hook
  (`migration-job.yaml`, a `batch/v1` pre-install/pre-upgrade hook with
  bounded `backoffLimit`/`activeDeadlineSeconds`).
- [x] Keep application startup independent from schema migration completion
  (the hook completes before release resources are created; the app's own
  idempotent Goose pass at startup remains as a non-K8s safety net).
- [x] Define expand/contract compatibility rules for migrations that span
  multiple application versions (chart README, including why Helm rollback
  does not run Goose down migrations).
- [x] Make migration status and failure diagnostics visible in deployment
  automation (chart README documents inspecting the migration Job and pod
  logs on failure).

**Acceptance:** a failed migration blocks the rollout without leaving a
partially available application deployment, and a successful migration can be
verified before the new application image receives traffic. Met.

### 6. Infrastructure policies ([#78](https://github.com/Sekai-World/sekai-master-api/issues/78))

- [x] Add PodDisruptionBudgets for both `control` and `serve` (opt-in
  templates with documented values).
- [x] Add NetworkPolicies to restrict inter-pod traffic to required paths
  (opt-in template with explicit ingress/egress rules).
- [x] Add topology spread constraints to avoid co-locating `control` and
  `serve` pods on the same node (`topologySpreadConstraints` wired into
  deployments and the migration Job; defaults empty).
- [ ] Enable image scanning/signing and automated rollback policy where the
  cluster supports them.
- [ ] Enforce TLS termination at the ingress layer; document certificate
  rotation (TLS is templated and documented; production enforcement and a
  production-safe values profile/schema remain open under
  [#78](https://github.com/Sekai-World/sekai-master-api/issues/78), which also
  asks for Helm lint/template tests of safe defaults).

**Acceptance:** infrastructure policies are enforced declaratively and
validated in CI, and the cluster rejects deployments that violate them. The
templates exist; enforcement via a production profile and CI validation is
still open ([#78](https://github.com/Sekai-World/sekai-master-api/issues/78)).

## P2 — Control-plane resilience

Goal: remove the current single-process coordination limit while preserving
single-writer behavior for synchronization.

### 7. Distributed synchronization coordination ([#80](https://github.com/Sekai-World/sekai-master-api/issues/80))

- [x] Replace process-local active-sync locking with PostgreSQL advisory locks,
  a Redis lease, a Kubernetes Lease, or an equivalent distributed coordinator.
  Delivered by #101: PostgreSQL lease table with atomic CAS acquisition
  (`#101`), exercised end-to-end by the real-PG takeover drill (#104).
- [x] Persist sync leases and ownership metadata and expose takeover state in
  diagnostics. Lease row persists holder/token/expiry (#101); admin diagnostics
  `GET /api/v1/admin/master-data/lease` plus lease gauges
  (`..._lease_held`, `..._lease_fencing_token`, `..._lease_expiry_unix`) and
  an observed-takeover counter (this change).
- [x] Add fencing tokens so an expired or partitioned worker cannot continue
  writing after another worker takes ownership. Delivered by #101 (strict
  fencing on PG status writes); validated live by the takeover drill (#104).
- [x] Make sync state transitions idempotent and safe across retries.
  Content-addressed Redis writes + fenced status inserts (#101).
- [x] Add leader-election or an equivalent worker model before increasing
  `control` replicas. The lease is the single-worker gate; the chart now
  allows `control.replicaCount > 1` only with `control.coordination.enabled`
  (test-environment capability; production keeps 1 replica + `Recreate`).

**Acceptance:** two `control` pods cannot concurrently own the same sync job;
an owner can fail and another pod can safely resume; stale owners cannot write
after lease loss. Proven by the real-PG drill documented in
[`docs/distributed-sync-coordination.md`](distributed-sync-coordination.md)
(#104): kill-the-holder takeover with monotonic tokens, webhook 409 +
`Retry-After`, zombie-holder fencing — zero stale-token rows persisted.

Design: [`docs/distributed-sync-coordination.md`](distributed-sync-coordination.md)
(delivered; PostgreSQL lease + durable fencing token).

### 8. Horizontally resilient control role

- [x] Remove the deployment requirement for `control.replicaCount: 1` once
  distributed coordination is proven. The schema now allows any
  `control.replicaCount >= 1` when `control.coordination.enabled` is `true`;
  the default and `values-production.yaml` keep 1 replica (personal-project
  deployment decision: prod never runs multi-replica control).
- [x] Replace the unconditional `Recreate` strategy with a safe rolling
  strategy where appropriate. `RollingUpdate` is used exactly when
  `control.coordination.enabled` is `true`; `Recreate` otherwise.
- [x] Test control-pod interruption during migrations, sync, and interrupted
  sync recovery. Covered by the real-PG takeover drill (#104): kill-holder,
  webhook-conflict, and zombie-holder scenarios plus in-place release
  verification.
- [x] Add takeover latency and failed-job recovery metrics. Delivered at
  lean scale: `sekai_master_data_sync_lease_takeovers` counter (observed
  fencing-token increases) plus the lease held/token/expiry gauges; latency
  is derivable from token/expiry timestamps in the lease diagnostics.

## P3 — SLO and recovery automation

Goal: turn observability and recovery capabilities into measurable service
objectives and repeatable operations.

### 9. SLOs and alerting ([#81](https://github.com/Sekai-World/sekai-master-api/issues/81))

Delivered at personal-project scale on purpose (per user decision, 2026-09-08):
no commercial-grade dashboards, drill programs, or trace storage.

- [x] Define availability and latency SLOs for public read endpoints
  (`docs/runbook.md` — availability ≥ 99% monthly, p95 < 1s, informal error
  budget).
- [x] Define freshness/sync-lag SLOs for each configured region
  (`docs/runbook.md` — successful sync within 48h per region).
- [x] Version a minimal alert set with runbook links
  (`deploy/observability/prometheus-rules.yaml`: region staleness, sync
  failure, stuck sync, target down, p95 latency, empty Redis data plane,
  missing search index; every alert links to a `docs/runbook.md` section via
  the `runbook` annotation). Rules are applied manually to the monitoring
  stack; they are not rendered by the chart.
- [x] Document recovery paths with exact commands, verification steps, and
  RTO/RPO targets (`docs/runbook.md`: Redis loss rebuild, PostgreSQL dump /
  restore, rollback, search-index repair).
- Skipped intentionally: dedicated Grafana dashboards (covered by the alert
  set plus the existing Grafana setup), durable Tempo storage (the deployed
  observability stack is dev-scale; `docs/production-observability-k3s.md`
  keeps the Tempo exporter guidance for when it is needed), and formal
  RTO/RPO drill records (quarterly drill cadence documented instead).

**Acceptance (lean):** every alert links to a runbook section, and each
recovery path names its command, verification step, and RTO/RPO target.

### 10. Delivery and recovery automation

- [x] Build immutable, versioned images. Delivered by the Phase 1 release
  pipeline (`docs/release.md`, `.github/workflows/release.yml`): a tag-driven
  workflow that publishes semver, `latest`, and `sha-*` tagged images to
  `docker.dnaroma.eu/sekai-world/sekai-master-api` with the pushed digest
  recorded in the release notes and step summary, enabling digest-pinned
  deployments and digest-based rollback. Provenance attestations and
  vulnerability scanning remain open follow-ups.
- [ ] Automate Helm validation, migration checks, deployment smoke tests, and
  rollback tests in CI/CD (CI currently runs lint, `go test`, and a Docker
  build; Helm lint/template coverage is also requested by
  [#78](https://github.com/Sekai-World/sekai-master-api/issues/78)).
- [x] Define a Redis-loss rebuild/restore drill (`scripts/redis-recovery-drill.sh`
  and the chart README recovery sequence).
- [x] Define a PostgreSQL backup restore drill (`docs/runbook.md` PostgreSQL
  restore path: dump → restore into the cluster with control scaled down, plus
  a quarterly scratch-database drill cadence; measured results are recorded
  when drills are run).
- [x] Document RTO/RPO targets (`docs/runbook.md`: Redis data plane RPO ≤ 60 s /
  RTO ≤ 30 min, PostgreSQL RPO ≤ 24h / RTO ≤ 1h, rollback RTO ≤ 15 min;
  periodic verification is a documented drill cadence rather than an automated
  program).
- [ ] Keep production configuration and infrastructure manifests versioned,
  reviewable, and separated from secret values (the chart keeps secrets
  external via `envFrom` and renders no plaintext credentials; versioning the
  production values themselves is operator-side).

**Acceptance:** a new version can be promoted, verified, rolled back, and
recovered using the delivery pipeline and documented runbooks without
ad-hoc shell changes in the cluster.

## Current architectural constraints to preserve until addressed

- `serve` is the horizontally scalable public-read role.
- `control` owns migrations, synchronization, webhook handling, and persisted
  search-index repair.
- `control` remains a single replica with `Recreate` deployment semantics
  until distributed locking and fencing are implemented (P2).
- `serve` readiness verifies PostgreSQL and Redis connectivity plus persisted
  cards and complete version metadata for every configured region. It remains
  a bounded, read-only probe and must not repair Redis data.
- Redis is a shared persisted data plane; `serve` must not silently rebuild an
  empty Redis.

## Source references

- `deploy/helm/sekai-master-api/README.md`
- `deploy/helm/sekai-master-api/templates/deployments.yaml`
- `deploy/helm/sekai-master-api/templates/migration-job.yaml`
- `deploy/helm/sekai-master-api/templates/pod-disruption-budgets.yaml`
- `deploy/helm/sekai-master-api/templates/network-policy.yaml`
- `deploy/helm/sekai-master-api/templates/_helpers.tpl`
- `deploy/helm/sekai-master-api/values.yaml`
- `scripts/smoke.sh`
- `scripts/redis-recovery-drill.sh`
- `docs/development.md`
- `docs/production-observability-k3s.md`
- `docs/release.md`
- `cmd/api/main.go`
- `cmd/api/shutdown.go`
- `internal/observability/otel.go`
- `internal/storage/migrate.go`
- `internal/transport/http/handlers/system/health_handler.go`
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
- [PR #82](https://github.com/Sekai-World/sekai-master-api/pull/82)
- [PR #86](https://github.com/Sekai-World/sekai-master-api/pull/86)
- [PR #87](https://github.com/Sekai-World/sekai-master-api/pull/87)
