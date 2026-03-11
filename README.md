# sekai-master-api

Go RESTful API template (Gin + Keycloak + environment-based database) with Dev Container support.

## Features

- Gin-based REST API (`/api/v1`)
- Keycloak access-token validation for admin-only API endpoints
- Environment-specific database strategy:
  - development: sqlite
  - test/production: postgresql
  - optional override via `DATABASE_DRIVER` (`sqlite`/`pgx`)
- Devcontainer for consistent development
- Compose-based test environment commands
- Third-party logging with Zap (configurable log level)
- Built-in modern admin dashboard with dedicated login page
- Master data sync from GitHub JSON repositories (multi-region)
- Master data query cache: Redis for by-id lookup, in-memory index for fuzzy name search (CJK supported)
- Sync status persisted in database (`master_data_sync_status`)
- Database schema migrations powered by Goose (`internal/storage/migrations`)

## Quick Start

1. Copy `.env.example` to `.env` and adjust values.
   - The app auto-loads `.env.<APP_ENV>` then `.env` (for example: `.env.test`, `.env.development`).
   - Environment variable precedence: shell env > `.env.<APP_ENV>` > `.env` > built-in defaults.
2. Install dependencies:
   - `go mod tidy`
3. Start API:
   - `make run`

### Local Test Env File

This repo provides `.env.test` for local testing. Typical flow:

1. Start dependencies on non-conflicting Keycloak port:
   - `KEYCLOAK_PORT=18081 make dev-env-up`
2. Run API in test mode:
   - `APP_ENV=test go run ./cmd/api`

For local development with PostgreSQL, use `.env.development` with `DATABASE_DRIVER=pgx`, then run:

- `make dev-env-up`
- `make dev-watch`
- `make format`
- `make swagger`

`make dev-watch` uses `air` for hot restart on Go code changes.
`make dev-watch` passes `LOKI_PUSH_URL` to the API process; the Go logger pushes app logs to Loki in-process (no external log-push script required).
Gin access/error logs are also routed through the same Zap pipeline, so they are pushed to Loki as well.
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
- `GET /api/v1/master-data/status`
- `GET /api/v1/master-data/events` (SSE stream for sync updates)
- `GET /api/v1/cards/:region/list?page=1&page_size=20`
- `GET /api/v1/cards/:region/search?q=<keyword>&field=name|skill&page=1&limit=20`
- `GET /api/v1/cards/:region/:id`
- `GET /api/v1/cards/:region/:id/params`
- `GET /api/v1/musics/:region/list?page=1&page_size=20`
- `GET /api/v1/musics/:region/search?title=<kw>&lyricist=<kw>&composer=<kw>&arranger=<kw>&page=1&limit=20`
  - provide at least one of `title/lyricist/composer/arranger`; when multiple are provided they are matched together
- `GET /api/v1/musics/:region/:id`
- `GET /api/v1/events/:region/current`
- `GET /api/v1/events/:region/:id`
- `GET /api/v1/events/:region/:id/rewards`
- `GET /api/v1/admin/profile` (Bearer token from Keycloak required)
- `POST /api/v1/admin/master-data/sync` (Bearer token from Keycloak required)
- `POST /api/v1/admin/master-data/sync/force` (Bearer token from Keycloak required)

`POST /api/v1/admin/master-data/sync` and `POST /api/v1/admin/master-data/sync/force` support optional JSON payload:

- `{ "region": "jp" }` for region-scoped sync
- empty payload for full-region sync

All non-admin `GET` API endpoints are public (no auth middleware).

Swagger UI is exposed at `GET /docs/index.html`, and OpenAPI JSON is exposed at `GET /docs/doc.json` only when `APP_ENV` is `development` or `test`.

## Master Data Sync

At startup, the API can sync parsed game database JSON files from one or more GitHub repositories into cache.

Startup sync runs in background after the API listener is up, so HTTP endpoints are available immediately.

- Region source repos are configured by env vars.
- Startup sync parallelism is controlled by `MASTER_DATA_SYNC_CONCURRENCY` (default `4`).
- Per-region file loading parallelism is controlled by `MASTER_DATA_REGION_FILE_CONCURRENCY` (default `8`).
- GitHub HTTP requests support retry via `MASTER_DATA_HTTP_RETRY_COUNT` (default `3`) and `MASTER_DATA_HTTP_RETRY_BACKOFF_MS` (default `300`).
- Each region can point to a different repository/ref/path.
- Sync result (success/failed, file count, last sync time, source info) is persisted in database table `master_data_sync_status`.
- Sync status storage model uses append-only history rows in `master_data_sync_status`; `created_at` is generated automatically by database default timestamp on insert. Latest status query uses database view `master_data_sync_status_latest` (one row per region) and exposes `last_updated_at` from that creation timestamp.
- Startup sync compares the region source commit against the most recent successful sync record per region (from history); if unchanged, it skips reload for that region.
- At sync start, if in-memory search index for a region is missing, region status is set to `pending` before sync proceeds.
- For changed regions, cache writes are applied incrementally (upsert changed records and remove deleted records), not full key flush.
- Region file loading uses retry + resumable rounds: already fetched files are kept, and only failed files are retried on subsequent attempts.
- Resume state is also persisted locally under `tmp/master-data-sync-resume/`, so restart/retry can continue from the last successful file instead of reloading all files.
- Successful sync payloads are temporarily backed up by region under `tmp/master-data-backup/<region>/latest/`, preserving all synced JSON files as directory snapshots.
- If commit is unchanged and previous sync status is success: API first tries rebuilding in-memory index from Redis; if Redis data is missing but local backup exists, it restores cache from local backup; otherwise it falls back to full sync.
- Sync status includes per-region sync duration (`sync_duration_ms`) for dashboard display.
- Sync status also includes `source_commit` for change tracking and skip decisions.
- You can inspect status via `GET /api/v1/master-data/status`.
- You can trigger manual sync from dashboard or call `POST /api/v1/admin/master-data/sync`.
- If you need to ignore commit comparison and force a full refresh, call `POST /api/v1/admin/master-data/sync/force`.

## Database Migrations

- Migrations are executed automatically on API startup via Goose.
- Migration files are located in `internal/storage/migrations`.
- Local helper commands:
   - `make migrate-up`
   - `make migrate-down`
- `make migrate-*` auto-resolves DB connection using `APP_ENV` + dotenv files (`.env.<APP_ENV>` then `.env`), with `DATABASE_DRIVER` override support.
- Example: `APP_ENV=test make migrate-up`

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
- `GET /api/v1/master-data/events` can notify frontend after sync finishes (`master_data_updated`)

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

1. Open `/admin/login` and enter Keycloak username/password.
2. Dashboard calls `POST /api/v1/admin/login` to exchange credentials for access token.
3. On success, redirects to `/admin` and calls `GET /api/v1/admin/profile`.

Master Data sync panel supports selecting target region (or all regions) and can run both normal sync and force sync per selected scope.

## Keycloak Integration

The API validates bearer tokens with Keycloak OIDC issuer metadata.

Required env vars:

- `KEYCLOAK_ISSUER_URL`
- `KEYCLOAK_AUDIENCE`

Optional flags for local troubleshooting:

- `KEYCLOAK_SKIP_ISSUER_CHECK`
- `KEYCLOAK_SKIP_AUDIENCE_CHECK`

## Local Keycloak (Dev)

This repository includes a minimal Keycloak setup with pre-imported realm/client/user.
Keycloak now runs in the same test stack compose file as PostgreSQL and Redis.

### Start / Stop

- `make keycloak-up`
- `make keycloak-logs`
- `make keycloak-down`

### Preloaded realm data

- realm: `sekai`
- client_id: `sekai-api`
- username: `alice`
- password: `alice123!`

### Get access token quickly

- `make keycloak-token`

The command returns token JSON from Keycloak token endpoint. Use the `access_token` as Bearer token for `GET /api/v1/admin/profile`.

If your API runs inside devcontainer while Keycloak runs on host Docker engine, set `KEYCLOAK_ISSUER_URL` to a host-reachable address in container network (for example `http://host.docker.internal:8081/realms/sekai` when available).

## Database by Environment

- `APP_ENV=development` uses sqlite at `SQLITE_PATH`.
- `APP_ENV=test` or `APP_ENV=production` uses postgresql via `DATABASE_URL`.
- `DATABASE_DRIVER` can override this behavior (`sqlite` or `pgx`).

Health endpoint returns both application and database status.

## Devcontainer + OrbStack / Docker Desktop

This project uses Unix socket mounting so devcontainer can call host Docker API.
The devcontainer installs Docker CLI and does not run daemon inside the devcontainer.
To avoid host-path mismatch when daemon is outside devcontainer, Keycloak realm import file is baked into a local test image during compose up (no bind mount for realm file).

### 1) Set socket variables before rebuilding Dev Container

Set host environment variables before opening/rebuilding container:

- `DOCKER_SOCK_PATH`: host Docker socket path to mount into devcontainer
- `DOCKER_SOCK_GID`: socket group id for devcontainer user access

Typical values on macOS OrbStack and Windows Docker Desktop:

- `DOCKER_SOCK_PATH=/var/run/docker.sock`
- `DOCKER_SOCK_GID=0`

If socket owner group is `root`, this value is usually `0`.

### 2) Rebuild Dev Container and verify

After container starts, validate from terminal in container:

- `echo $DOCKER_HOST`
- `docker version`
- `docker compose version`

Codex CLI is also available in devcontainer:

- `codex --help`
- Config file is editable at `.devcontainer/config.toml`
- The devcontainer links it to `~/.codex/config.toml`

If you see `permission denied while trying to connect to docker socket`, verify both variables are exported before rebuilding devcontainer:

- `echo $DOCKER_SOCK_PATH`
- `echo $DOCKER_SOCK_GID`

## Test environment

Use compose commands through Makefile (`postgres:18-alpine`, `redis:8-alpine`, `keycloak:26.1.4`):

- Makefile uses `docker compose` (fallback: `docker-compose`)

- `make dev-env-up`
- `make dev-env-logs`
- `make dev-env-down`

`make dev-env-down` now preserves named volumes by default.
If you need a full cleanup including volumes, use:

- `make dev-env-down-purge`

`make dev-env-up` also starts Grafana + Loki for Go app log search (`dev-watch` output):

- Grafana URL: `http://localhost:${GRAFANA_PORT}` (default `http://localhost:3000`)
- Quick open: `make dev-logs-ui`
- API logs are pushed to Loki in-process via `LOKI_PUSH_URL` (default `http://host.docker.internal:${LOKI_PORT}/loki/api/v1/push`)

Legacy aliases remain available:

- `make test-env-up`
- `make test-env-logs`
- `make test-env-down`
- `make test-env-down-purge`

End-to-end local smoke check:

- `make smoke`

`scripts/smoke.sh` defaults dependency host to `localhost`, but when running inside a container it will prefer `host.docker.internal` if resolvable (fallback `host.containers.internal`). You can override explicitly with `COMPOSE_HOST`.
If host `8081` is occupied, set `KEYCLOAK_PORT` (for example `18081`) before running `make dev-env-up` or `make smoke`.
