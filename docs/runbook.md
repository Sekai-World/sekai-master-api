# Operations Runbook

Personal-project scale: this runbook defines the service objectives, the
minimal alert set, and the recovery paths for sekai-master-api. It replaces
the commercial-grade SLO/dashboards/drill-program requirements from the
roadmap's P3 phase with a leaner equivalent. Alerts live in
[`deploy/observability/prometheus-rules.yaml`](../deploy/observability/prometheus-rules.yaml)
and are applied manually to the monitoring stack.

## SLOs

| Objective | Target | Measured by |
| --- | --- | --- |
| Public read availability | ≥ 99% monthly | `up{job="sekai-master-api"}` / probe failures |
| Public read latency | p95 < 1s | `sekai_http_server_request_duration_ms` histogram |
| Master-data freshness | every configured region has a successful sync within 48h | `sekai_master_data_region_last_synced_unix{region}` |

Error budget is tracked informally: if an objective is breached in a month,
investigate and fix before adding features; no formal budget accounting.

## Alerts → runbook sections

| Alert | Meaning | Runbook |
| --- | --- | --- |
| `SekaiMasterDataRegionStale` | a region has not synced successfully in 48h | [Master data freshness](#master-data-freshness) |
| `SekaiMasterDataSyncFailed` | sync status is `error` for a region | [Sync failed or stuck](#sync-failed-or-stuck) |
| `SekaiMasterDataSyncStuck` | sync running > 6h | [Sync failed or stuck](#sync-failed-or-stuck) |
| `SekaiPublicReadDown` | scrape target down 5m | [Public read endpoint down](#public-read-endpoint-down) |
| `SekaiPublicReadLatencyHigh` | p95 latency > 1s for 15m | [Public read endpoint down](#public-read-endpoint-down) |
| `SekaiRedisDataPlaneEmpty` | Redis has no keys | [Redis loss rebuild](#redis-loss-rebuild) |
| `SekaiSearchIndexNotReady` | fewer loaded indexes than synced regions | [Search index not ready](#search-index-not-ready) |

## Recovery paths

Each path lists the exact command(s), the verification step, and the target
objectives. RTO/RPO targets are intentionally modest for a personal service.

### Master data freshness

A region exceeding the 48h freshness target usually means the upstream
repository has no new commits (benign) or the sync is failing.

1. Check current status: open the admin dashboard (`/admin`) and look at
   per-region sync status, or `GET /api/v1/master-data/status`.
2. Check upstream: does the source repository have recent commits? If not,
   no action is needed — the alert will clear after the next sync or can be
   silenced.
3. Trigger a sync: `POST /api/v1/admin/master-data/sync` (admin bearer token
   required), or restart the control pod to trigger the startup sync.
4. Verify: status turns `success` and the region's `last_synced` timestamp is
   current.

### Sync failed or stuck

1. Inspect logs: `kubectl logs -l app.kubernetes.io/component=control -c serve` —
   or the compose/Grafana equivalent — and look for sync error messages.
2. Common causes: upstream GitHub API failures, PostgreSQL connectivity, or
   (since #80) the sync lease being held by another pod
   (`ErrSyncLeaseHeld`, webhook responds 409 with `Retry-After`).
3. If a lease is stuck beyond its TTL (default 60s with 20s heartbeats), the
   next acquire takes over automatically; only intervene if the takeover
   itself fails.
4. Verify: region status returns to `success`/`pending` and
   `sekai_master_data_sync_running` returns to 0 after the run.

### Public read endpoint down

1. Check pod state: `kubectl get pods -l app.kubernetes.io/name=sekai-master-api`.
2. Check readiness: `kubectl describe pod <pod>` — `/readyz` fails when
   PostgreSQL/Redis are unreachable or a region has no persisted records.
3. Fix the dependency first (see Redis loss rebuild / PostgreSQL restore), then
   the pods recover on their own.
4. Rollback if a recent rollout broke it (see Rollback).
5. Verify: `scripts/smoke.sh <public-url>` passes.

### Redis loss rebuild

Redis is the persisted data plane; the app never silently rebuilds an empty
Redis from PostgreSQL.

- Recovery objective: **RPO ≤ 60s, RTO ≤ 30min** (documented in the
  [chart README](../deploy/helm/sekai-master-api/README.md)).
- Command: `scripts/redis-recovery-drill.sh` with the environment variables it
  requires (`REDIS_ADDR`, `MASTER_DATA_REDIS_KEY_PREFIX`,
  `REDIS_RECOVERY_{REGION,PUBLIC_URL,SERVE_URL,CONTROL_URL}`,
  `ADMIN_BEARER_TOKEN`, and
  `REDIS_RECOVERY_DRILL_CONFIRM=DELETE_PREFIXED_REDIS_DATA`).
- Verify: the script itself validates region data on the public URL, readiness
  on serve/control, and reports the elapsed time.

### PostgreSQL restore

- Recovery objective: **RPO ≤ 24h, RTO ≤ 1h** (a daily dump is sufficient for
  a personal deployment).
- Backup (run daily, e.g. a CronJob):
  `pg_dump "$DATABASE_URL" -Fc -f /backup/sekai-master-api-$(date +%F).dump`
- Restore:
  1. Stop the control role (it owns migrations and sync):
     `kubectl scale deploy/sekai-master-api-control --replicas=0`.
  2. `pg_restore --clean --if-exists -d "$DATABASE_URL" <dump-file>`.
  3. Restart control: `kubectl scale deploy/sekai-master-api-control --replicas=1`.
- Verify: `GET /api/v1/master-data/status` returns the pre-incident region
  statuses; serve pods turn ready; `scripts/smoke.sh` passes.

### Rollback

- Recovery objective: **RTO ≤ 15min**.
- Command: `helm rollback <release> <previous-revision>` — or redeploy the
  previous image digest, which is recorded in each release's notes and step
  summary (see `docs/release.md`).
- Verify: `scripts/smoke.sh <public-url>` passes and the admin dashboard
  reports the expected version.

### Search index not ready

Read-only status paths never repair Redis search indexes, so a missing index
is fixed only through explicit flows:

1. Trigger an ensure/warmup sync for the affected region via the admin
   dashboard, or run a full sync (`POST /api/v1/admin/master-data/sync`).
2. Verify: `sekai_master_data_region_index_loaded{region="<r>"} == 1` resumes.

## Drill cadence

- Run the Redis recovery drill after major data-plane changes and at least
  quarterly; record the measured RTO in this file or the release notes.
- Run a PostgreSQL restore drill (dump → restore into a scratch database)
  at least quarterly.
- Exercise a rollback whenever the release pipeline changes.
