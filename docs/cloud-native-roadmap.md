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

The main remaining gap is the control plane: synchronization, migrations,
Redis recovery, and lifecycle state are not yet independently coordinated or
fully automated.

## Phase 1 — Production Kubernetes baseline

Goal: make the current two-role architecture safe to operate repeatedly in a
cluster without relying on undocumented manual sequencing.

### 1. External dependency durability

- [ ] Use managed or highly available PostgreSQL and Redis in production.
- [ ] Define Redis persistence (AOF/RDB), backup, restore, retention, and
  recovery objectives.
- [ ] Run and document a Redis-loss recovery drill using `control` force sync.
- [ ] Monitor Redis/PostgreSQL connectivity, saturation, and error rates.

**Acceptance:** a Redis replacement can be restored or rebuilt through a
documented, repeatable operation, and the resulting data/version state is
verified before public traffic is enabled.

### 2. Separate schema migration from application rollout

- [ ] Run Goose migrations from a dedicated Kubernetes Job or release hook.
- [ ] Keep application startup independent from schema migration completion.
- [ ] Define expand/contract compatibility rules for migrations that span
  multiple application versions.
- [ ] Make migration status and failure diagnostics visible in deployment
  automation.

**Acceptance:** a failed migration blocks the rollout without leaving a
partially available application deployment, and a successful migration can be
verified before the new application image receives traffic.

### 3. Automate control-to-serve rollout sequencing

- [ ] Represent master-data initialization/sync completion as an explicit
  rollout condition rather than an operator-only instruction.
- [ ] Keep `serve` readiness tied to usable persisted region data, not merely
  process and PostgreSQL availability.
- [ ] Add deployment smoke tests for public reads, admin access, webhook
  handling, and the admin SSE stream.
- [ ] Add PodDisruptionBudgets, NetworkPolicies, image scanning/signing, and
  automated rollback policy where the cluster supports them.

**Acceptance:** a fresh deployment cannot route public traffic to an empty or
unready Redis data plane, and a failed rollout automatically stops or rolls
back with an actionable signal.

## Phase 2 — Control-plane resilience

Goal: remove the current single-process coordination limit while preserving
single-writer behavior for synchronization.

### 4. Distributed synchronization coordination

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

### 5. Horizontally resilient control role

- [ ] Remove the deployment requirement for `control.replicaCount: 1` once
  distributed coordination is proven.
- [ ] Replace the unconditional `Recreate` strategy with a safe rolling
  strategy where appropriate.
- [ ] Test control-pod interruption during migrations, sync, and interrupted
  sync recovery.
- [ ] Add takeover latency and failed-job recovery metrics.

**Acceptance:** a control-pod replacement completes without manual cleanup,
duplicate sync ownership, or inconsistent persisted status.

## Phase 3 — Operational maturity

Goal: turn observability and recovery capabilities into measurable service
objectives and repeatable operations.

### 6. SLOs and alerting

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

### 7. Delivery and recovery automation

- [ ] Build immutable, versioned images with provenance and vulnerability
  scanning.
- [ ] Automate Helm validation, migration checks, deployment smoke tests, and
  rollback tests in CI/CD.
- [ ] Define backup restore drills for PostgreSQL and Redis.
- [ ] Document RTO/RPO targets and verify them periodically.
- [ ] Keep production configuration and infrastructure manifests versioned,
  reviewable, and separated from secret values.

**Acceptance:** a new version can be promoted, verified, rolled back, and
recovered using the delivery pipeline and documented runbooks without
ad-hoc shell changes in the cluster.

## Suggested priority order

1. Redis durability and recovery drill.
2. Independent migration Job and expand/contract rules.
3. Readiness/rollout gating plus smoke tests.
4. Distributed sync lock, lease, and fencing.
5. Control-pod takeover and horizontal resilience.
6. SLOs, alerts, dashboards, and recovery automation.

## Current architectural constraints to preserve until addressed

- `serve` is the horizontally scalable public-read role.
- `control` owns migrations, synchronization, webhook handling, and persisted
  search-index repair.
- Redis is a shared persisted data plane; `serve` must not silently rebuild an
  empty Redis.
- `control` must remain a single replica with `Recreate` deployment semantics
  until distributed locking and fencing are implemented.

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
