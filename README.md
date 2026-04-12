# sekai-master-api

Go RESTful API template (Gin + OIDC + environment-based database).

## Features

- Gin-based REST API (`/api/v1`)
- OIDC access-token validation for protected API endpoints
- Environment-specific database strategy:
  - development: sqlite
  - test/production: postgresql
  - optional override via `DATABASE_DRIVER` (`sqlite`/`pgx`)
- Compose-based development environment commands for PostgreSQL, Redis, Keycloak, Grafana, Loki, Tempo, Prometheus, and the OpenTelemetry Collector
- Third-party logging with Zap (configurable log level)
- Built-in modern admin dashboard with dedicated login page
- Master data sync from GitHub JSON repositories (multi-region)
- Master data query cache: Redis for by-id lookup, in-memory index for fuzzy name search (CJK supported)
- Sync status persisted in database (`master_data_sync_status`)
- Database schema migrations powered by Goose (`internal/storage/migrations`)

## Quick Start

1. Copy `.env.example` to `.env` and adjust values.
   - The app auto-loads dotenv files in precedence order (highest precedence first): `.env.<APP_ENV>.local`, `.env.local`, `.env.<APP_ENV>`, then `.env` (for example: `.env.development.local`, `.env.development`).
   - Later files do not override variables already set by earlier files. Effective precedence is: shell env > `.env.<APP_ENV>.local` > `.env.local` > `.env.<APP_ENV>` > `.env` > built-in defaults.
2. Install dependencies:
   - `go mod tidy`
3. Start API:
   - `make run`

### Test Env File

This repo provides `.env.test` for connecting to the deployed test environment. You can keep machine-specific overrides in `.env.local` or `.env.test.local`. `APP_ENV=test` is expected to connect directly to your remote test PostgreSQL and OIDC endpoints; it does not start local compose services.

For local development with PostgreSQL and the bundled support services, use `.env.development` with `DATABASE_DRIVER=pgx`. Put machine-specific overrides in `.env.development.local` if needed, then run:

- `make dev-env-up`
- `make dev`
- `make format`
- `make swagger`

`make dev-env-up` starts PostgreSQL, Redis, Keycloak, Grafana, Loki, Tempo, Prometheus, and the OpenTelemetry Collector. Compose health checks are configured for PostgreSQL, Redis, Loki, and Grafana, and the local Keycloak realm/client/user are pre-imported in the Keycloak image.

`make dev` now builds a dedicated app image with `docker buildx build --load`, ensures the dev dependency stack is up, and runs the API as its own container on the same `sekai-dev` Docker network.
The image bakes in repository `.env*` files, and the container entrypoint writes a `.env.development.local` override so the app talks to `postgres`, `redis`, `otel-collector`, `loki`, and `keycloak` over the internal Docker network instead of `host.docker.internal`.
The app container publishes `http://localhost:8080` by default and mounts a dedicated named volume at `/app/tmp` so SQLite files, master-data backups, and rebuilt caches can persist across restarts.

Useful local commands:

- `make dev`
- `make dev-logs`
- `make dev-down`

The Go logger pushes app logs to Loki in-process (no external log-push script required), Gin access/error logs follow the same Zap pipeline, and the API exports traces and metrics through OTLP/HTTP to the local OpenTelemetry Collector for Prometheus and Tempo backed Grafana dashboards.
`make format` applies `gofmt` to all Go files.
`make swagger` regenerates Swagger docs from Go annotations.

GitHub Actions CI runs `gofmt` check, `go vet ./...`, and `go test ./...` on every push and pull request.

Logging level can be configured by env var `LOG_LEVEL` (for example: `debug`, `info`, `warn`, `error`).
If `LOG_LEVEL` is empty, default is `debug` for non-production envs and `info` for production.

## API Endpoints

- `GET /docs`
- `GET /docs/index.html`
- `GET /docs/doc.json`
- `GET /api/v1/health`
- `GET /api/v1/cards/:region/list?page=1&page_size=20`
- `GET /api/v1/cards/:region/search?q=<keyword>&field=name|skill&page=1&limit=20`
- `GET /api/v1/cards/:region/:id`
- `GET /api/v1/cards/:region/:id/params`
- `GET /api/v1/cards/:region/:id/episodes`
- `GET /api/v1/musics/:region/list?page=1&page_size=20`
- `GET /api/v1/musics/:region/search?title=<kw>&lyricist=<kw>&composer=<kw>&arranger=<kw>&page=1&limit=20`
  - provide at least one of `title/lyricist/composer/arranger`; when multiple are provided they are matched together
- `GET /api/v1/musics/:region/:id`
- `GET /api/v1/events/:region/current`
- `GET /api/v1/events/:region/:id`
- `GET /api/v1/events/:region/:id/rewards`
- `GET /api/v1/admin/profile` (Bearer token from configured OIDC provider required)
- `GET /api/v1/admin/master-data/events` (Bearer token from configured OIDC provider required; dashboard SSE uses `access_token` query param)
- `GET /api/v1/admin/master-data/status` (Bearer token from configured OIDC provider required)
- `POST /api/v1/admin/master-data/sync` (Bearer token from configured OIDC provider required)
- `POST /api/v1/admin/master-data/sync/force` (Bearer token from configured OIDC provider required)

`POST /api/v1/admin/master-data/sync` and `POST /api/v1/admin/master-data/sync/force` support optional JSON payload:

- `{ "region": "jp" }` for region-scoped sync
- empty payload for full-region sync

All non-admin `GET` API endpoints are public (no auth middleware).

Swagger UI is exposed at `GET /docs/index.html`, and OpenAPI JSON is exposed at `GET /docs/doc.json` only when `APP_ENV` is `development` or `test`.

## Master Data Sync

At startup, the API can sync parsed game database JSON files from one or more GitHub repositories into cache.

Startup sync runs in background after the API listener is up, so HTTP endpoints are available immediately.

- Region source repos are configured by env vars.
- `MASTER_DATA_RECOVER_INTERRUPTED_SYNC` controls whether startup should detect stale `running` / `pending` latest statuses and retry only those interrupted regions.
- Startup sync parallelism is controlled by `MASTER_DATA_SYNC_CONCURRENCY` (default `4`).
- `MASTER_DATA_REGION_FILE_CONCURRENCY` is retained for backward-compatible configuration, but archive-based sync no longer performs per-file GitHub fetches.
- GitHub HTTP requests support retry via `MASTER_DATA_HTTP_RETRY_COUNT` (default `3`) and `MASTER_DATA_HTTP_RETRY_BACKOFF_MS` (default `300`).
- Each region can point to a different repository/ref/path.
- Sync result (success/failed, file count, last sync time, source info) is persisted in database table `master_data_sync_status`.
- Sync status storage model uses append-only history rows in `master_data_sync_status`; `created_at` is generated automatically by database default timestamp on insert. Latest status query uses database view `master_data_sync_status_latest` (one row per region) and exposes `last_updated_at` from that creation timestamp.
- Startup sync compares the region source commit against the most recent successful sync record per region (from history); if unchanged, it skips reload for that region.
- At sync start, if in-memory search index for a region is missing, region status is set to `pending` before sync proceeds.
- For changed regions, cache writes are applied incrementally (upsert changed records and remove deleted records), not full key flush.
- For changed regions, the loader downloads a single GitHub tarball archive for the resolved commit and extracts only JSON files under the configured path, which reduces request count and avoids per-file raw fetch rate limits.
- Temporary archive workspace is created under `tmp/master-data-sync-resume/` during sync and removed after the region load completes.
- Successful sync payloads are temporarily backed up by region under `tmp/master-data-backup/<region>/latest/`, preserving all synced JSON files as directory snapshots.
- If commit is unchanged and previous sync status is success: API first tries rebuilding in-memory index from Redis; if Redis data is missing but local backup exists, it restores cache from local backup; otherwise it falls back to full sync.
- In development, if local backup files exist for a configured region but sync status is missing from the database, startup can restore cache and rebuild indexes directly from the latest local backup before any remote sync runs.
- If the process restarts while a region is still marked `running` or `pending`, startup recovery can automatically retry only those interrupted regions.
- Interrupted sync recovery reuses the normal sync path, so unchanged regions can still be restored from Redis or local backup instead of forcing a full refetch.
- Sync status includes per-region sync duration (`sync_duration_ms`) for dashboard display.
- Sync status also includes `source_commit` for change tracking and skip decisions.
- Admin dashboard reads a region-scoped status view from `GET /api/v1/admin/master-data/status`, including configured regions and current sync-running state.
- Sync progress and completion events are exposed only via `GET /api/v1/admin/master-data/events`.
- You can trigger manual sync from dashboard or call `POST /api/v1/admin/master-data/sync`.
- If you need to ignore commit comparison and force a full refresh, call `POST /api/v1/admin/master-data/sync/force`.

## Database Migrations

- Migrations are executed automatically on API startup via Goose.
- Migration files are located in `internal/storage/migrations`.
- Local helper commands:
   - `make migrate-up`
   - `make migrate-down`
- `make migrate-*` auto-resolves DB connection using `APP_ENV` + dotenv files (`.env`, `.env.<APP_ENV>`, `.env.local`, `.env.<APP_ENV>.local`), with later files overriding earlier ones.
- Example: `APP_ENV=test make migrate-up`
  This will target whatever remote test database is configured in `.env.test` / `.env.test.local`.

### Required env setup pattern

1. Set region list:
   - `MASTER_DATA_REGIONS=jp,global`
2. Configure each region source:
   - `MASTER_DATA_GITHUB_OWNER_<REGION>`
   - `MASTER_DATA_GITHUB_REPO_<REGION>`
   - `MASTER_DATA_GITHUB_REF_<REGION>`
   - `MASTER_DATA_GITHUB_PATH_<REGION>`

`<REGION>` uses uppercase, non-alphanumeric chars replaced by `_`.

Example for `jp`:

- `MASTER_DATA_GITHUB_OWNER_JP=Sekai-World`
- `MASTER_DATA_GITHUB_REPO_JP=sekai-master-data-jp`
- `MASTER_DATA_GITHUB_REF_JP=main`
- `MASTER_DATA_GITHUB_PATH_JP=data`

### Query cache strategy

- card by-id query reads from Redis hash cache (`region + cards + id`)
- card params query reuses the same cached card record and only returns params-related fields
- card search supports `field=name|skill`: `name` maps to `prefix`, `skill` maps to `cardSkillName`
- music search uses field keyword params: `title`, `lyricist`, `composer`, `arranger` (at least one required)
- music multi-field search matches records that satisfy all provided field keywords
- music response maps `creatorArtistId` → `creatorArtist` (lookup from `musicArtists.json` by `id`) and removes `creatorArtistId` from response
- music response maps `liveStageId` → `liveStage` (lookup from `liveStages.json` by `id`) and removes `liveStageId` from response
- card response maps `cardSupplyId` → `cardSupply`, `skillId` → `skill`, `characterId` → `character`, `cardRarityType` → `cardRarity`
- `character` is loaded from `gameCharacters.json` and excludes `live2dHeightAdjustment`, `figure`, `breastSize`, `modelName`
- card query endpoints return `503` with `REGION_DATA_NOT_READY` when region sync status is not `success`
- card search response includes `pagination` (`page`, `page_size`, `total`, `total_pages`, `has_next`) same as list
- card list pagination follows real `cards.json` array order (data index), not id continuity
- card list pagination response includes `total_pages` and `has_next`
- event by-id endpoint omits `eventRankingRewardRanges` from the main payload
- current event endpoint (`GET /api/v1/events/:region/current`) reads from Redis cache first, validates event time window, and refreshes cache from `events.json` when cached event is expired/missing
- event rewards endpoint returns `eventRankingRewardRanges` via `GET /api/v1/events/:region/:id/rewards`
- `GET /api/v1/admin/master-data/events` can notify frontend after sync finishes (`master_data_updated`)

Redis settings:

- `REDIS_ADDR`
- `REDIS_PASSWORD`
- `REDIS_DB`
- `MASTER_DATA_REDIS_KEY_PREFIX`

### Optional GitHub token

Set `MASTER_DATA_GITHUB_TOKEN` if you need higher GitHub API rate limit.

## Admin Dashboard

- `GET /admin/login` (dashboard login page)
- `GET /admin` (dashboard home)
- Open login page quickly: `make admin-open` (default `APP_PORT=8080`)

Login flow:

1. Open `/admin/login`.
2. Dashboard redirects the browser to the configured OIDC authorization endpoint.
3. After the OIDC provider redirects back to `/api/v1/admin/login/callback`, the backend exchanges the authorization code with PKCE. If `OIDC_PRIVATE_KEY_PATH` is configured it also sends `private_key_jwt`; otherwise it behaves as a public client.
4. On success, the callback page stores the access token in session storage, redirects to `/admin`, and the dashboard calls `GET /api/v1/admin/profile`.

Master Data sync panel supports selecting target region (or all regions) and can run both normal sync and force sync per selected scope.

## OIDC Integration

The API validates bearer tokens with OIDC issuer metadata. The admin login flow always uses authorization code plus PKCE, and can optionally add `private_key_jwt` client authentication when a private key is configured.

Required env vars:

- `OIDC_ISSUER_URL`
- `OIDC_AUDIENCE`
- `OIDC_CLIENT_ID`
- `OIDC_REDIRECT_URL`
- `OIDC_SCOPES`

Optional flags for local troubleshooting:

- `OIDC_AUTH_URL`
- `OIDC_TOKEN_URL`
- `OIDC_SKIP_ISSUER_CHECK`
- `OIDC_SKIP_AUDIENCE_CHECK`
- `OIDC_PRIVATE_KEY_PATH`
- `OIDC_PRIVATE_KEY_ID`
- `OIDC_ADMIN_CLAIM`
- `OIDC_ADMIN_CLAIM_VALUES`

`OIDC_SCOPES` should include the standard OpenID scopes required by your provider and any API audience/resource scopes required by your deployment.

Optional admin RBAC:

- If `OIDC_ADMIN_CLAIM` and `OIDC_ADMIN_CLAIM_VALUES` are both set, `/api/v1/admin/*` requires the validated token to contain at least one matching claim value.
- Claim lookup supports top-level or dotted paths such as `groups`, `roles`, or `realm_access.roles`.
- Claim values can be arrays (`["sekai-admin"]`) or strings. `scope` / `scp` string claims are split on whitespace and commas.
- If these env vars are left empty, admin authorization falls back to authenticated-user-only behavior.
- The admin profile endpoint and dashboard only expose matched-claim debug details in `development` / `test`; production hides them even when RBAC is enabled.

For local development, `.env.development` is preconfigured for the bundled Keycloak instance:

- Keycloak URL: `http://localhost:18081`
- OIDC issuer: `http://localhost:18081/realms/sekai`
- OIDC internal issuer URL: `http://host.docker.internal:18081/realms/sekai`
- OIDC client ID / audience: `sekai-api`
- OIDC redirect URI: `http://localhost:8080/api/v1/admin/login/callback`
- Admin RBAC claim: `groups`
- Required admin value: `sekai-admin`

`make dev-env-up` also starts Keycloak with a pre-imported local realm, client, admin group, and test login user:

- Test login user: `alice`
- Test login password: `alice123!`
- Keycloak bootstrap admin: `admin`
- Keycloak bootstrap admin password: `admin`

To fetch a local access token without going through the browser login flow, you can run:

- `make keycloak-token`

If you need a different local setup, override the `KEYCLOAK_*` and `OIDC_*` values in `.env.development.local`.

For smoke checks against protected endpoints, provide a valid `ADMIN_BEARER_TOKEN` from your OIDC test environment. `make smoke` no longer starts local dependencies.

## Database by Environment

- `APP_ENV=development` uses sqlite at `SQLITE_PATH`.
- `APP_ENV=test` or `APP_ENV=production` uses postgresql via `DATABASE_URL`.
- `DATABASE_DRIVER` can override this behavior (`sqlite` or `pgx`).

Health endpoint returns both application and database status.

## Local Docker

Local development assumes a host Docker engine with both `docker compose` semantics and `docker buildx`.

Useful checks:

- `docker version`
- `docker compose version`
- `docker buildx version`

## Test environment

Use compose commands through Makefile (`postgres:18-alpine`, `redis:8-alpine`, `grafana`, `loki`, `tempo`, `prometheus`, `otel-collector`):

- Makefile uses `docker compose` (fallback: `docker-compose`)

- `make dev-env-up`
- `make dev-env-logs`
- `make dev-env-down`

`make dev-env-down` now preserves named volumes by default.
If you need a full cleanup including volumes, use:

- `make dev-env-down-purge`

`make dev-env-up` also starts a local observability stack for the Go app, and `make dev` connects the app container to it automatically:


- Grafana URL: `http://localhost:${GRAFANA_PORT}` (default `http://localhost:13000`)
- Quick open: `make dev-logs-ui`
- `make run` uses host-facing defaults such as `http://host.docker.internal:${LOKI_PORT}/loki/api/v1/push`; `make dev` rewrites the app container to use internal service URLs such as `http://loki:3100/loki/api/v1/push`
- Prometheus URL: `http://localhost:${PROMETHEUS_PORT}` (default `http://localhost:9090`)
- Tempo API URL: `http://localhost:${TEMPO_PORT}` (default `http://localhost:3200`)
- `make run` uses `http://host.docker.internal:${OTEL_COLLECTOR_HTTP_PORT}` by default; `make dev` rewrites the app container to use `http://otel-collector:4318`
- Grafana provisions Loki, Prometheus, Tempo datasources and a `Sekai / Sekai API Observability` dashboard automatically
- The default dashboard includes HTTP throughput/latency, Go runtime memory and goroutines, Redis memory and key count, plus per-region master-data sync/index metrics such as status, synced file count, indexed record count, and approximate index size

End-to-end local smoke check:

- `make smoke`

`make smoke` requires `ADMIN_BEARER_TOKEN` to already be set in the shell and only validates the API process you start locally against the configured external services.
