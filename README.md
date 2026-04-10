# sekai-master-api

Go RESTful API template (Gin + OIDC + environment-based database) with Dev Container support.

## Features

- Gin-based REST API (`/api/v1`)
- OIDC access-token validation for protected API endpoints
- Environment-specific database strategy:
  - development: sqlite
  - test/production: postgresql
  - optional override via `DATABASE_DRIVER` (`sqlite`/`pgx`)
- Devcontainer for consistent development
- Compose-based development environment commands for PostgreSQL, Redis, Authentik, Grafana, and Loki
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

### Devcontainer Workflow

If you develop through the repository devcontainer, prefer the Makefile helpers so the workspace path stays consistent:

- `make devcontainer-up`
- `make devcontainer-test`

If the host SSH agent socket changed and `devcontainer up` fails with a stale bind-mount error, rebuild the container instead of reusing the old one:

- `make devcontainer-rebuild`

### Test Env File

This repo provides `.env.test` for connecting to the deployed test environment. You can keep machine-specific overrides in `.env.local` or `.env.test.local`. `APP_ENV=test` is expected to connect directly to your remote test PostgreSQL and OIDC endpoints; it does not start local compose services.

For local development with PostgreSQL and the bundled support services, use `.env.development` with `DATABASE_DRIVER=pgx`. Put machine-specific overrides in `.env.development.local` if needed, then run:

- `make dev-env-up`
- `make dev-watch`
- `make format`
- `make swagger`

`make dev-env-up` starts PostgreSQL, Redis, Authentik, Grafana, and Loki. Compose health checks are configured for the local support services, and the bootstrap step waits for the local Authentik OIDC issuer to become ready.

`make dev-watch` uses `air` for hot restart on Go code changes.
For devcontainer workflows with limited memory, `make dev-watch` now defaults to a lower-memory profile:

- `MASTER_DATA_AUTO_SYNC=false`
- `GOFLAGS=-p=1`
- `GOMEMLIMIT=1500MiB`
- `GOGC=50`

This avoids repeated startup sync and reduces Go build peak memory inside 8G containers. If you need the old behavior or want to tune it, override the Make variables explicitly, for example:

- `make dev-watch DEV_WATCH_MASTER_DATA_AUTO_SYNC=true`
- `make dev-watch DEV_WATCH_GOFLAGS='-p=2' DEV_WATCH_GOMEMLIMIT=2GiB DEV_WATCH_GOGC=100`

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
- `GET /api/v1/cards/:region/:id/episodes`
- `GET /api/v1/musics/:region/list?page=1&page_size=20`
- `GET /api/v1/musics/:region/search?title=<kw>&lyricist=<kw>&composer=<kw>&arranger=<kw>&page=1&limit=20`
  - provide at least one of `title/lyricist/composer/arranger`; when multiple are provided they are matched together
- `GET /api/v1/musics/:region/:id`
- `GET /api/v1/events/:region/current`
- `GET /api/v1/events/:region/:id`
- `GET /api/v1/events/:region/:id/rewards`
- `GET /api/v1/admin/profile` (Bearer token from configured OIDC provider required)
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
- Sync status includes per-region sync duration (`sync_duration_ms`) for dashboard display.
- Sync status also includes `source_commit` for change tracking and skip decisions.
- You can inspect status via `GET /api/v1/master-data/status`.
- Admin dashboard reads a region-scoped status view from `GET /api/v1/admin/master-data/status`, including configured regions and current sync-running state.
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

`OIDC_SCOPES` should include the standard OpenID scopes required by your provider and any API audience/resource scopes required by your deployment.

For local development, `.env.development` is preconfigured for the bundled Authentik instance:

- Authentik URL: `http://localhost:19100`
- OIDC issuer: `http://localhost:19100/application/o/sekai-admin-web/`
- OIDC client ID / audience: `sekai-admin-web`
- OIDC redirect URI: `http://localhost:8080/api/v1/admin/login/callback`

`make dev-env-up` also provisions, via Authentik blueprints, a local OIDC application and a test login user:

- Test login user: `admin`
- Test login password: `Admin123!Admin123!`
- Authentik bootstrap admin: `akadmin`
- Authentik bootstrap admin password: `Admin123!Admin123!`

The local Authentik instance reuses the existing development `postgres` and `redis` services. By default it stores its PostgreSQL tables in the same local `sekai` database, and uses dedicated Redis DB indexes so it does not collide with the API cache keys.

If you need a different local setup, override the `AUTHENTIK_*` and `OIDC_*` values in `.env.development.local`.

For smoke checks against protected endpoints, provide a valid `ADMIN_BEARER_TOKEN` from your OIDC test environment. `make smoke` no longer starts local dependencies.

## Database by Environment

- `APP_ENV=development` uses sqlite at `SQLITE_PATH`.
- `APP_ENV=test` or `APP_ENV=production` uses postgresql via `DATABASE_URL`.
- `DATABASE_DRIVER` can override this behavior (`sqlite` or `pgx`).

Health endpoint returns both application and database status.

## Devcontainer + OrbStack / Docker Desktop

This project uses Unix socket mounting so devcontainer can call host Docker API.
The devcontainer installs Docker CLI and does not run daemon inside the devcontainer.

### 1) Set socket variables before rebuilding Dev Container

Set host environment variables before opening/rebuilding container:

- `DOCKER_SOCK_PATH`: host Docker socket path to mount into devcontainer
- `DOCKER_SOCK_GID`: socket group id for devcontainer user access
- `SSH_AUTH_SOCK`: host SSH agent socket path to mount into devcontainer

Typical values on macOS OrbStack and Windows Docker Desktop:

- `DOCKER_SOCK_PATH=/var/run/docker.sock`
- `DOCKER_SOCK_GID=0`

For SSH agent forwarding, the devcontainer uses `${SSH_AUTH_SOCK}` from host if present.
If it is not exported, the mount falls back to `/run/host-services/ssh-auth.sock`, which works on Docker Desktop / OrbStack host-service sockets.
On Linux or any custom SSH agent setup, export the current host socket path before rebuilding:

- `echo $SSH_AUTH_SOCK`
- `ssh-add -l`

If socket owner group is `root`, this value is usually `0`.

### 2) Rebuild Dev Container and verify

After container starts, validate from terminal in container:

- `echo $DOCKER_HOST`
- `echo $SSH_AUTH_SOCK`
- `docker version`
- `docker compose version`
- `ssh-add -l`

Codex CLI is also available in devcontainer:

- `codex --help`
- Config file is editable at `.devcontainer/config.toml`
- The devcontainer links it to `~/.codex/config.toml`

For repeatable local setup from the host terminal, you can also use:

- `make devcontainer-up`
- `make devcontainer-rebuild`
- `make devcontainer-test`

If you see `permission denied while trying to connect to docker socket`, verify both variables are exported before rebuilding devcontainer:

- `echo $DOCKER_SOCK_PATH`
- `echo $DOCKER_SOCK_GID`

If `ssh-add -l` inside devcontainer says it cannot connect to agent, confirm the host SSH agent is running and `SSH_AUTH_SOCK` is exported before rebuild.
The devcontainer does not mount host private key files directly; it forwards the agent socket only.
If you need SSH host verification inside container, populate `~/.ssh/known_hosts` in the container as usual.
If `devcontainer up` fails with `invalid mount config for type "bind"` and the missing path points to an old launchd socket under `/private/tmp/com.apple.launchd.*`, the previous container likely captured a stale `SSH_AUTH_SOCK`; run `make devcontainer-rebuild` after confirming the current `SSH_AUTH_SOCK` exists.

## Test environment

Use compose commands through Makefile (`postgres:18-alpine`, `redis:8-alpine`, `grafana`, `loki`):

- Makefile uses `docker compose` (fallback: `docker-compose`)

- `make dev-env-up`
- `make dev-env-logs`
- `make dev-env-down`

`make dev-env-down` now preserves named volumes by default.
If you need a full cleanup including volumes, use:

- `make dev-env-down-purge`

`make dev-env-up` also starts Grafana + Loki for Go app log search (`dev-watch` output):


- Grafana URL: `http://localhost:${GRAFANA_PORT}` (default `http://localhost:13000`)
- Quick open: `make dev-logs-ui`
- API logs are pushed to Loki in-process via `LOKI_PUSH_URL` (default `http://host.docker.internal:${LOKI_PORT}/loki/api/v1/push`)

End-to-end local smoke check:

- `make smoke`

`make smoke` requires `ADMIN_BEARER_TOKEN` to already be set in the shell and only validates the API process you start locally against the configured external services.
