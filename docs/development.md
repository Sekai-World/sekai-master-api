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

Development defaults to SQLite unless `DATABASE_DRIVER=pgx` is set. Test and production default to PostgreSQL.

## Local Development

For host-mode API development:

```sh
mise run run
```

For the default lightweight dependency stack:

```sh
mise run dev-env-up
mise run dev
```

`mise run dev-env-up` starts only PostgreSQL, Redis, and Keycloak. The default app container runs with `OTEL_ENABLED=false`, no Loki push URL, and no OTLP endpoint, so local development does not require the observability stack.

`mise run dev` builds and runs the API container on the `sekai-dev` Docker network. The app container publishes `http://localhost:18080` by default and uses internal service URLs for PostgreSQL, Redis, and Keycloak.

For metrics-only local observability:

```sh
mise run dev-env-up-metrics
mise run dev-metrics
```

This starts PostgreSQL, Redis, Keycloak, Prometheus, and a metrics-only Alloy collector. It enables OTEL metrics export from the app without Loki or Tempo. The app sets `OTEL_TRACING_ENABLED=false`, so it does not install HTTP tracing middleware or export spans to the metrics-only collector.

For the full local observability stack:

```sh
mise run dev-env-up-full
mise run dev-full
```

This starts PostgreSQL, Redis, Keycloak, Loki, Tempo, Prometheus, Alloy, and Grafana, and runs the app with Loki and OTLP endpoints configured.

Useful commands:

- `mise run dev-logs`
- `mise run dev-down`
- `mise run dev-env-logs`
- `mise run dev-env-down`
- `mise run dev-env-down-purge`

## Docker Requirements

Local development assumes a host Docker engine with Compose and Buildx:

```sh
docker version
docker compose version
docker buildx version
```

The default compose project name is `sekai-master-api`. Override it with:

```sh
COMPOSE_PROJECT_NAME=your-name mise run dev-env-up
```

## Migrations

Migrations run automatically on API startup through Goose.

Manual helpers:

```sh
mise run migrate-up
mise run migrate-down
```

`mise run migrate-*` resolves the database from `APP_ENV`, dotenv files, and `DATABASE_DRIVER`.

Example:

```sh
APP_ENV=test mise run migrate-up
```

Migration files live in `internal/storage/migrations`.

## Observability

Logging uses Zap. Configure level with `LOG_LEVEL` (`debug`, `info`, `warn`, `error`). If empty, the default is `debug` outside production and `info` in production.

Local observability is opt-in. With OrbStack, common URLs are:

- Grafana: `http://grafana.sekai-master-api.orb.local`
- Prometheus: `http://prometheus.sekai-master-api.orb.local`
- Tempo: `http://tempo.sekai-master-api.orb.local`

Open Grafana quickly:

```sh
mise run dev-logs-ui
```

Grafana is available only after `mise run dev-env-up-full`; metrics-only mode uses Prometheus directly.

Grafana provisions Loki, Prometheus, Tempo datasources and dashboards for API/network, runtime memory, and master-data metrics.

`OTEL_ENABLED` controls OpenTelemetry metrics initialization. `OTEL_TRACING_ENABLED` controls trace exporter/provider setup and HTTP tracing independently; when omitted it inherits `OTEL_ENABLED` for backward compatibility. Set tracing to `false` when the configured collector does not expose a traces pipeline.

## Smoke Check

```sh
mise run smoke
```

`mise run smoke` requires `ADMIN_BEARER_TOKEN` for protected endpoint checks.

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
`standalone`, so plain `mise run run` / `mise run dev` stays compatible with the
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
| Search-index warmup (local decoded-index load) | ✅ | ❌ | ❌ (persisted built during sync) |
| Master-data auto-sync / interrupted-sync recovery | ✅ | ❌ | ✅ |
| Startup readiness | After migrations | Immediate | After migrations |

> `control` skips the startup decoded-index warmup (`EnsureConfiguredRegionIndexes`):
> it never serves public read/search traffic, so decoding persisted Redis indexes
> into control process memory is wasted work. Persisted Redis search indexes are
> (re)built by sync / force-sync in `control` and by warmup in `standalone`, so
> skipping it for `control` does not remove any persisted-index repair behavior.

### Local split ports

`mise run dev` remains the monolithic standalone development path. To run the
roles split locally on distinct ports (with the dev dependency stack):

```sh
mise run dev-split          # runs serve (APP_PORT_SERVE, default 18080) + control (APP_PORT_CONTROL, default 18081)
```

`dev-split` starts **two separate host processes** (`run-serve` and `run-control`)
sharing the same Redis/Postgres dependency stack; it does not run the container
`standalone` image. For the existing single-container dev path use `mise run dev`.

or individually:

```sh
mise run run-serve          # public read/query, APP_PORT_SERVE (default 18080)
mise run run-control        # admin UI/API + lifecycle ownership, APP_PORT_CONTROL (default 18081)
```

### OIDC redirect consideration

The admin login redirect (`OIDC_REDIRECT_URL`) points at the callback path on
the control role's port in a split deployment. When splitting, set
`OIDC_REDIRECT_URL` to the `control` port (for example
`http://localhost:18081/api/v1/admin/login/callback`). The `serve` role never
mounts the admin login callback, so it must not receive the OIDC redirect.

### Redis as a shared data plane

Redis is the shared data plane for both persisted master-data records and
persisted search indexes. The behaviors by role:

- `serve` only reads from Redis. It never syncs or repairs an empty Redis. In a
  split deployment, `serve` must not receive traffic until `control` has
  populated Redis (via sync/auto-sync), otherwise public endpoints return
  `503`/`404` data errors.
- `control` owns writes: master-data sync, force-sync, migrations, and
  search-index (re)build persist into the shared Redis.
- Production deployments must use a persistent/managed Redis (or enable AOF/RDB
  persistence and backups). A `serve` replica with an empty Redis cannot recover
  it on its own; a lost Redis is recovered by running **force sync from
  `control`** (`POST /api/v1/admin/master-data/sync/force`), which rebuilds the
  persisted indexes and records.

### Role-specific decoded-index LRU cache

The in-process decoded search-index LRU (bounded by
`MASTER_DATA_SEARCH_INDEX_CACHE_ENTRIES`) is a local read cache used only by
read/search traffic:

- `serve` and `standalone` keep the configured capacity.
- `control` disables it (capacity `0`) because it never serves public read
  traffic; this avoids needlessly decoding persisted Redis indexes into the
  control process. The disablement is centralized in
  `Config.EffectiveSearchIndexCacheEntries()` and applied when the Redis cache is
  constructed at startup.

### Startup order and readiness limitation

`/api/v1/health` checks process liveness and database connectivity only; it does
**not** verify that region master-data / Redis indexes are ready. In a split
deployment, `control` must populate data (sync or auto-sync) before `serve`
receives traffic — `serve` becomes ready immediately but will return data errors
until Redis is populated. Coordinate rollout so `control` completes (or at least
begins) sync before routing public traffic to `serve`.

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
to). Only `standalone` and `control` own migrations, search-index build, and
sync; `serve` is stateless with respect to those lifecycle jobs.

### Local dependency reset and re-sync

`mise run dev-env-down` removes the **non-persistent** Redis container and its
data, while the Postgres data volume persists. After a Redis reset, `serve`
would see an empty Redis while Postgres still reports prior versions — a mismatch
that resolves only after `control` re-populates Redis. Run force sync from
`control` (`POST /api/v1/admin/master-data/sync/force`) to rebuild persisted
records and search indexes, or bring Redis back before tearing down Postgres.
