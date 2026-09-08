# Distributed Sync Coordination and Fencing — Design

Status: proposed (tracked by
[#80](https://github.com/Sekai-World/sekai-master-api/issues/80)).

This document specifies how `control` acquires cross-pod ownership of
master-data synchronization, how ownership is renewed and taken over, and how
a stale or partitioned owner is prevented from writing after another pod has
taken over. It is the design gate before any implementation for the
roadmap's P2 item "Distributed synchronization coordination".

## Problem

All sync admission today is process-local:

- `MasterDataSyncUsecase.syncRunning` (`internal/usecase/master_data_sync_usecase.go`)
  is an in-process `atomic.Bool`; a second `control` pod admits its own sync
  independently.
- The admission gate (`admitMu`/`admissionClosed`/`syncWG`) only coordinates
  against graceful shutdown inside one process.
- The chart therefore schema-locks `control.replicaCount: 1` with a
  `Recreate` strategy, and every upgrade or node drain of the single control
  pod serializes behind a full sync interruption + recovery cycle.

Sync entry points that must become cross-pod mutually exclusive: startup
`SyncAll` and `RecoverInterruptedSync` (`cmd/api/main.go`), admin `StartSync`
(detached worker), and webhook `SyncRegion`.

## Requirements (from #80)

1. Two `control` pods cannot concurrently own the same sync job.
2. A failed owner is replaced without manual cleanup; takeover state is
   visible in diagnostics.
3. A stale owner cannot write after losing ownership (fencing).
4. Sync state transitions are idempotent and safe across retries/restarts.
5. Owner failure, lease expiry, and takeover are exercised against real
   PostgreSQL/Redis dependencies.
6. Only after these guarantees are proven may the chart allow control
   rolling updates or `replicaCount > 1`.

## Coordinator choice

| Option | Verdict | Reason |
|--------|---------|--------|
| PostgreSQL lease table + durable fencing token | **chosen** | PG is already a hard dependency of `control`, already holds the authoritative sync status (`master_data_sync_status` + history + latest view), and survives Redis loss — the exact disaster the coordinator must stay correct through. Transactions give atomic compare-and-swap acquisition and in-transaction fencing checks. |
| Redis lease (`SET NX PX`) + `INCR` fencing counter | rejected as primary | The fencing counter would live in the datastore that Redis-loss recovery rebuilds. After a Redis wipe the counter restarts and a partitioned stale owner's old (high) token would compare as valid again, reopening the exact window fencing exists to close. |
| Kubernetes Lease | rejected | Adds `k8s.io/client-go` + RBAC, couples coordination to cluster machinery while the app already runs split-role on plain hosts (`mise run dev-split`), and still needs a separate durable fencing counter. |

Redis remains the data plane; it just does not host the coordinator.

## Lease protocol

### Storage

One new Goose migration:

```sql
CREATE TABLE master_data_sync_leases (
  name               TEXT PRIMARY KEY,          -- lease scope, e.g. 'master-data-sync'
  holder             TEXT NOT NULL,             -- instance identity
  fencing_token      BIGINT NOT NULL,           -- monotonic, issued on each acquisition
  acquired_at        TIMESTAMP NOT NULL,
  expires_at         TIMESTAMP NOT NULL,        -- acquired_at/last renew + ttl
  last_heartbeat_at  TIMESTAMP NOT NULL
);
```

A single lease scope (`master-data-sync`) covers all sync entry points,
preserving today's "one active sync job per deployment" semantics across
pods. Region-scoped leases are a deliberate non-goal: region syncs share the
Redis data plane, status history, and backup store, and partial-region
concurrency across pods buys little for the added split-brain surface.

Instance identity: `os.Hostname()` (pod name) plus process boot timestamp,
e.g. `sekai-master-api-control-7d9f…/2026-09-08T12:00:00Z`.

### Operations

All three are single-statement compare-and-swaps (Postgres via
`INSERT ... ON CONFLICT ... WHERE` / `UPDATE ... RETURNING`; the repository
keeps the existing SQLite/Postgres driver split so development mode works
unchanged):

- **Acquire(name, holder, ttl) → (token, ok)**: succeeds when the row is
  absent or `expires_at <= now()`. On success the row is (re)written with a
  fresh `fencing_token = fencing_token + 1`. Token issuance is therefore
  atomic with ownership transfer — two racers cannot both win.
- **Renew(name, holder, ttl) → ok**: extends `expires_at` only when the
  caller is still the holder. Renewal failure means ownership is gone (or PG
  is unreachable — treated the same: stop writing).
- **Release(name, holder)**: best-effort delete-if-holder on graceful
  completion/shutdown. Failure to release is safe: the lease simply expires.

TTL default 60s; the running job renews every TTL/3 (20s) from a heartbeat
goroutine tied to the job context.

### Job integration

`sync()`/`StartSync()` keep their process-local gates (they remain useful
for shutdown admission) and additionally:

1. `Acquire` the lease before admitting the job; `ErrSyncInProgress`-style
   "lease held elsewhere" skips the job (webhooks return a retryable
   conflict; startup syncs wait for the next tick/recovery pass).
2. On acquisition, start the heartbeat goroutine; heartbeats share the job
   context and a dedicated bounded PG timeout.
3. Heartbeat failure (renewal lost or unreachable) cancels the job context:
   the existing graceful-interruption path then leaves per-region statuses
   in recoverable form — the same semantics as a shutdown interruption
   today, reused for self-fencing.
4. `Release` on completion; the deferred release never errors the job.

Takeover is passive-by-expiry plus active-on-start: a starting pod that
finds an expired lease acquires it and runs `RecoverInterruptedSync` first,
so a half-finished region set from the dead owner is completed before new
work is admitted.

## Fencing model

The fencing token makes "am I still allowed to write?" checkable at the
durable stores, not just in the holder's memory:

1. **PostgreSQL status writes — strict fencing.** `SyncStatus` rows gain the
   token that wrote them. The status repository's Save runs in one
   transaction: `SELECT fencing_token FROM master_data_sync_leases WHERE
   name = $1 FOR UPDATE`, reject when the writer's token is older than the
   lease's current token. This protects the authoritative record of
   "last successful sync per region" — the input to skip decisions,
   readiness, and the dashboard — with an airtight storage-side check.
2. **Redis data-plane writes — phase-boundary fencing plus idempotent
   shapes.** Per-key token checks on Redis writes are not proposed. Instead
   the job re-verifies lease ownership at each region-phase boundary
   (before load, before cache store, before version-cache store); writes
   between boundaries are bounded by the phase durations and the job
   timeout. Safety of a bounded stale write is provided by the existing
   content-addressed write shapes: entity records/order are guarded by
   revision and source-digest comparisons
   (`StoreRegionWithSourceDigests`), version payloads are keyed by commit.
   A stale writer can at worst re-write content its own earlier phase
   loaded; the next authoritative owner's revision/digest compare detects
   and corrects divergence on the next sync.
3. **Local backup store — guarded by content addressing.** Backup payload
   directories are commit-named; a stale write lands as an unreferenced
   snapshot and is ignored by commit-pinned loads.

Residual risk, stated plainly: between phase boundaries a partitioned stale
owner may still write Redis. The window is bounded (phase-level, not
unbounded), the written content is commit/revision-addressed so it cannot
masquerade as newer data, and the PG status fence prevents it from ever
being recorded as successful. This is the standard pragmatic fencing
boundary (strict check at the source-of-truth store; idempotency + checks
at derived stores) and it is what the acceptance tests will demonstrate.

## Idempotency

- Lease acquisition/renewal/release are CAS operations; retries are safe.
- Status transitions remain region-keyed upserts; with the token guard they
  become "write only if mine is the current lease" — retrying after
  takeover correctly fails instead of double-writing.
- Sync payloads are commit-addressed end to end (Redis records by revision,
  version payloads by commit, backups by commit), so a re-run after
  interruption converges rather than duplicates.

## Failure scenarios

| Scenario | Behavior |
|----------|----------|
| Owner pod crashes mid-sync | Lease expires after TTL; next pod acquires with token+1, runs `RecoverInterruptedSync` (statuses for the dead owner's regions are still `running`/`pending` and recoverable), completes the set. |
| Owner partitioned from PG, still running | Heartbeat renew fails → job context cancelled → same recoverable-interruption path. Writes it attempted afterwards fail the PG fence; Redis-side effect bounded as above. |
| Owner partitioned from PG *and* Redis | Nothing to write; cancellation on next phase boundary/heartbeat failure. |
| Redis loss during owned sync | Existing behavior: sync fails or falls back per region; lease is untouched (it lives in PG), owner may retry/force-sync without a lease flap. |
| PG failover during heartbeat | Renewal fails → owner self-cancels; after failover a pod re-acquires. Token durability follows PG durability — by design the strongest available. |
| Two pods start simultaneously | One Acquire wins (single row, atomic token bump); loser skips/waits. |

## Observability

- Metrics: `sync_lease_state` (gauge: held/expired/free), `sync_lease_token`
  (gauge), counters for acquisitions, renewals, renewal failures, takeovers
  (acquisition after expiry with different holder), and fenced-write
  rejections.
- Admin dashboard: lease holder identity, token, `expires_at`, seconds
  since last heartbeat, last takeover time.
- Logs: lease events with `holder`/`token` fields on the existing
  master-data-sync component.

## Chart and rollout (implementation phase 2, gated)

1. Ship the coordinator with `control.replicaCount` still locked to 1 and
   `Recreate`. Single-pod correctness and recovery drills first.
2. Add `control.coordination.enabled` (default `false`). When `true`, the
   schema permits `replicaCount >= 2` and the control Deployment switches to
   a RollingUpdate strategy (surge 1, unavailable 0). Takeover then makes
   rollouts safe without the migration-hook-era manual sequencing.
3. `helm-verify` gains renders asserting: coordination disabled keeps
   today's constraints; enabled renders the relaxed ones; and the negative
   case (replicas > 1 with coordination off) is still schema-rejected.

## Testing plan

- Repository integration tests against real PostgreSQL (and SQLite for dev
  parity): acquire/expire/takeover monotonic tokens, renew-after-takeover
  failure, fenced status-write rejection.
- Usecase tests with two instances sharing one PG: concurrent `sync()` →
  exactly one admitted; kill-the-holder simulation (stop heartbeating) →
  takeover completes regions the dead owner left `running`.
- Race builds on the coordination packages.
- The real-PG/Redis drills from the issue are run against the dev
  dependency stack and documented in the runbook.

## Configuration

- `MASTER_DATA_SYNC_LEASE_ENABLED` (default `true` on the `control` role;
  `serve` never syncs and never leases).
- `MASTER_DATA_SYNC_LEASE_TTL` (default `60s`, minimum 3× the heartbeat
  interval).
- `MASTER_DATA_SYNC_LEASE_NAME` (default `master-data-sync`).
- Heartbeat interval is `TTL/3`, not separately configurable.
- Entries land in `.env.example`; docs in `docs/master-data.md`.

## Resolved decisions

1. Webhook `SyncRegion` while the lease is held elsewhere **returns a
   conflict with `Retry-After`** (HTTP 409 on the webhook endpoint).
   Queueing was rejected: it invites silent dedup complexity and hides
   propagation lag behind an internal buffer.
2. Force sync **does not steal an unexpired lease faster than TTL**.
   The TTL is short by default; stealing would introduce a second clock
   assumption and a race between "holder is unhealthy" judgments made by
   different pods.

## Source references

- `internal/usecase/master_data_sync_usecase.go` — process-local admission
  (`syncRunning`, `tryAdmitSyncWorker`), `syncClaimed` phases
- `internal/repository/master_data_sync_status_repository.go` — status
  store (driver-split), fencing integration point
- `internal/storage/migrations/` — status schema/history/latest view
- `internal/storage/master_data_redis_cache.go` — content-addressed write
  shapes (`StoreRegionWithSourceDigests` revision/digest guards)
- `cmd/api/main.go` — startup `SyncAll`/`RecoverInterruptedSync` wiring
- `deploy/helm/sekai-master-api/values.schema.json` — `control.replicaCount`
  lock and rollout gating
- `docs/cloud-native-roadmap.md` — P2 goals and acceptance criteria
