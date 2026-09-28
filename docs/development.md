# Development

## Environment

Copy `.env.example` to `.env` and adjust values.

Dotenv load precedence is:

1. Shell environment
2. `.env.<APP_ENV>.local`
3. `.env.local`
4. `.env.<APP_ENV>`
5. `.env`
6. Built-in defaults

PostgreSQL is the only supported database in every environment: set
`DATABASE_URL`. SQLite support was removed; `DATABASE_DRIVER` is optional and
accepts only `pgx`, so a leftover `DATABASE_DRIVER=sqlite` fails startup
instead of silently switching databases.

The process opens one `pgxpool` pool; the `database/sql` repositories (sync
status and lease), health checks, and migrations share it through a bridge.
Size and timeouts come from `DATABASE_MAX_CONNS`, `DATABASE_MIN_CONNS`,
`DATABASE_CONNECT_TIMEOUT_SECONDS`, `DATABASE_MAX_CONN_LIFETIME_SECONDS`, and
`DATABASE_MAX_CONN_IDLE_SECONDS`; `0` keeps the `pool_*`/`connect_timeout`
parameters of `DATABASE_URL` or the pgx defaults.

## Local Development

For host-mode API development (needs PostgreSQL through `DATABASE_URL`):

```sh
mise run run
```

To run the roles individually on the host (they need a reachable PostgreSQL
through `DATABASE_URL`):

```sh
mise run run-serve          # public read/query, APP_PORT_SERVE (default 18080)
mise run run-control        # admin UI/API + lifecycle ownership, APP_PORT_CONTROL (default 18081)
```

The standard development/testing path is the **remote-cluster workflow**:
`mise run dev-cluster-rebuild` builds a ko image and deploys it to the remote
k3s test cluster next to the dev PostgreSQL, and
`mise run dev-cluster-forward` forwards the single public-API + admin port to
`http://localhost:18080`. These tasks are gitignored because they encode
private environment details (see `.mise/lib/dev-cluster.sh` and `AGENTS.md`).

The former Docker Compose dev stack (local app container, local
PostgreSQL/Redis/Keycloak, local observability stack) has been removed. Do not
reintroduce local app containers for serving or testing changes;
`deploy/compose/app/` only holds the Dockerfile used by CI image builds.

## Tests Without a Host Go Toolchain

`mise run test` uses the host `go` when available. Without one, it falls back
to `mise run test-docker`, which runs `go test ./...` in a container via
`scripts/docker-go.sh` with cached module/build volumes. That fallback requires
a host Docker engine.

## PostgreSQL Tests

Storage and repository tests run against real PostgreSQL through the
`internal/storage/pgtest` harness: the first test that needs a database starts
one `postgres:18` container per test binary with `testcontainers-go` on the
host Docker API, and every test gets its own fresh database. A package that
uses the harness calls `pgtest.Main` from its `TestMain`.

- Without Docker these tests skip with a message, unless `CI` is set: CI
  (GitHub Actions sets `CI=true`) fails instead, so they always run there.
- `PGTEST_DATABASE_URL` points the tests at an existing server instead of a
  container; the user needs permission to create databases.
- Registry mirrors work through the standard testcontainers settings, for
  example `TESTCONTAINERS_HUB_IMAGE_NAME_PREFIX=mirror.gcr.io/`.
- The image is Debian-based on purpose: its default collation is not byte
  order, so tests notice a missing `COLLATE "C"`.
- `mise run test-docker` runs Go inside a container without the Docker socket,
  so the PostgreSQL tests skip there.

## Migrations

Migrations run automatically on API startup through Goose.

Manual helpers:

```sh
mise run migrate-up
mise run migrate-down
```

`mise run migrate-*` reads `DATABASE_URL` from the dotenv files of `APP_ENV`.
All migrations are PostgreSQL-only.

Example:

```sh
APP_ENV=test mise run migrate-up
```

Migration files live in `internal/storage/migrations`.

## Observability

Logging uses Zap. Configure level with `LOG_LEVEL` (`debug`, `info`, `warn`, `error`). If empty, the default is `debug` outside production and `info` in production.

`OTEL_ENABLED` controls OpenTelemetry metrics initialization. `OTEL_TRACING_ENABLED` controls trace exporter/provider setup and HTTP tracing independently; when omitted it inherits `OTEL_ENABLED` for backward compatibility. Set tracing to `false` when the configured collector does not expose a traces pipeline.

Production observability (metrics, dashboards, log shipping) is deployed on k3s; see [Production Observability on K3s](production-observability-k3s.md).

## Smoke Check

```sh
mise run smoke
```

`mise run smoke` requires `ADMIN_BEARER_TOKEN` for protected endpoint checks.

For a deployed endpoint, supply an already-running base URL and a concrete
public data path. The script waits on `/startupz` and `/readyz` before checking
the public read:

```sh
SMOKE_SERVE_BASE_URL=http://localhost:18080 \
SMOKE_PUBLIC_PATH=/api/v1/versions/jp \
mise run smoke
```

Set `SMOKE_CHECK_PROTECTED=true`, `SMOKE_CONTROL_BASE_URL`, and
`ADMIN_BEARER_TOKEN` only when real OIDC configuration is available. This adds
serve/control public-route separation, unauthenticated admin-SSE and webhook
rejection checks, and an authenticated admin profile request; it does not invent
a dummy OIDC issuer.

The smoke check uses `CURL_CONNECT_TIMEOUT_SECONDS` (default `5`) and
`CURL_MAX_TIME_SECONDS` (default `15`) to bound individual HTTP requests. Each
value must be a positive whole-second number from `1` through `60`; the script
rejects invalid or larger values.

For recovery paths and drills, see [Runbook](runbook.md).

## Runtime Roles

The server can run in one of three roles, selected either by the first
positional subcommand or by the `APP_ROLE` environment variable. Both forms are
equivalent:

```sh
./sekai-master-api standalone   # or: APP_ROLE=standalone
./sekai-master-api serve        # or: APP_ROLE=serve
./sekai-master-api control      # or: APP_ROLE=control
```

An unrecognized subcommand (or no subcommand at all) falls back to
`standalone`, so plain `mise run run` stays compatible with the
previous monolithic behavior.

### Role composition

| Surface | `standalone` | `serve` | `control` |
| --- | --- | --- | --- |
| Public read/query API (`/api/v1` cards, musics, events, gachas, virtualLives, lookups, versions) | ✅ | ✅ | ❌ (404) |
| Admin UI (`/admin`, `/admin/login`) | ✅ | ❌ (404) | ✅ |
| OIDC-protected admin API (`/api/v1/admin/*`, including sync endpoints) | ✅ | ❌ (404) | ✅ |
| Internal GitHub webhook (`/api/v1/internal/github/webhooks/master-data`) | ✅ | ❌ (404) | ✅ |
| Health check (`/api/v1/health`) | ✅ | ✅ | ✅ |
| Swagger UI (`/docs`, dev/test only) | ✅ | ✅ | ✅ |
| Migrations on startup | ✅ | ❌ | ✅ |
| Master-data auto-sync / interrupted-sync recovery | ✅ | ❌ | ✅ |
| Startup readiness | After migrations | Immediate | After migrations |

### Split host runs

`run-serve` and `run-control` start **two separate host processes** on distinct
ports (`APP_PORT_SERVE`, default 18080, and `APP_PORT_CONTROL`, default 18081)
sharing the same PostgreSQL; this mirrors the production split
deployment without containers.

### OIDC redirect consideration

The admin login redirect (`OIDC_REDIRECT_URL`) points at the callback path on
the control role's port in a split deployment. When splitting, set
`OIDC_REDIRECT_URL` to the `control` port (for example
`http://localhost:18081/api/v1/admin/login/callback`). The `serve` role never
mounts the admin login callback, so it must not receive the OIDC redirect.

### PostgreSQL as the shared data plane

PostgreSQL holds the master data, its relation indexes and list projections,
and sync status and leases (see
[the store design](postgres-master-data-store.md)). The behaviors by role:

- `serve` only reads. It never syncs or repairs missing data. In a split
  deployment, `serve` must not receive traffic until `control` has written the
  data (via sync/auto-sync), otherwise public endpoints return `503` data
  errors.
- `control` owns writes: master-data sync, force-sync, and migrations.
- A region missing from the store is recovered by running **force sync from
  `control`** (`POST /api/v1/admin/master-data/sync/force`); the source
  repositories on GitHub are the canonical copy of every record.

### Startup order and readiness limitation

`/api/v1/health` checks process liveness and database connectivity only; it does
**not** verify that region master data is ready. In a split
deployment, `control` must populate data (sync or auto-sync) before `serve`
receives traffic. Although `serve` completes its process startup immediately,
its `/readyz` probe remains `503` until every configured region has a successful
persisted sync and stored card records. Coordinate rollout around
`/startupz` followed by `/readyz`, not `/api/v1/health`.

### `control` must remain a single replica

Sync history is persisted in Postgres, but the active-sync lock and
`sync_running` state are process-local in the current implementation. Running
more than one `control` replica can cause concurrent syncs and conflicting
active-state reporting. Deploy `control` with
`replicas: 1` and a `Recreate` strategy until distributed locking/fencing is
added. `serve` can be scaled horizontally behind a load balancer because it is
stateless with respect to lifecycle jobs.

### Ingress routing

In a split deployment:

- Public API ingress → `serve` (and `/docs` if you want Swagger exposed publicly;
  otherwise keep `/docs` only on `control` or disable it in production).
- Admin ingress → `control` for `/admin`, `/admin/login`, `/api/v1/admin/*`. This
  ingress is the host the OIDC provider is allowed to redirect to
  (`OIDC_REDIRECT_URL`).
- Internal GitHub webhook ingress → `control` at
  `/api/v1/internal/github/webhooks/master-data` (restrict by source IP / secret).
- The admin SSE stream `GET /api/v1/admin/master-data/events` is served by
  `control`. Proxy it with SSE-friendly settings: disable response buffering
  (`X-Accel-Buffering: no` on nginx/ingress-nginx), raise proxy read/timeout to
  cover long-lived connections, and avoid compressing the stream.

### Deployment implications

The container entrypoint defaults to `standalone`, preserving the existing
single-process deployment (the entrypoint explicitly sets `APP_ROLE=standalone`).
A split deployment runs `serve` behind the public ingress and `control` behind a
restricted admin ingress (the same host the OIDC provider is allowed to redirect
to). Only `standalone` and `control` own migrations and sync; `serve` is stateless with respect to those lifecycle jobs.
