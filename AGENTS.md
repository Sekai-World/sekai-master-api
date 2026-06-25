# AGENTS.md

## Purpose

This file defines collaboration boundaries, execution order, and acceptance criteria for AI/automation agents in this repository.

## Project Context

- Project type: Golang RESTful API using Gin.
- Authentication: OIDC Bearer Token validation.
- Authentication boundary: only admin APIs require authentication; other GET APIs are public by default.
- Query strategy:
  - `cards` by-id: Redis hash cache.
  - `cards` fuzzy name search: in-memory index using the current `prefix` field.
  - `cards` list pagination: paginate by real data order, using array index; do not rely on contiguous IDs.
  - If a response has a top-level `releaseConditionId`, look up `releaseConditions` and expand it as `releaseCondition`; do not expose `releaseConditionId` directly.
- Database strategy:
  - Default: development uses SQLite; test and production use PostgreSQL.
  - Optional override: `DATABASE_DRIVER` can override the default with `sqlite` or `pgx`.
- Migration strategy: use Goose SQL migrations; run automatic migrations on startup.
- Local dependency orchestration: `deploy/compose/dev-compose.yaml` for PostgreSQL 18, Redis 8, Grafana, and Loki.

## Agent Roles

### 1) API Agent

Responsible for HTTP routes, handlers, middleware, and response structures.

- Change scope: `cmd/api`, `internal/transport/http`.
- Must preserve:
  - Route prefix `/api/v1`.
  - Unified error response format.
  - Protected endpoints are validated only through OIDC Bearer Token authentication.
  - Non-admin GET APIs must not mount authentication middleware.
  - The admin dashboard provides a dedicated login page and must not introduce a local username/password account system.
  - `cards` query endpoints remain specialized; do not fall back to generic entity query endpoints.
  - The SSE notification endpoint `GET /api/v1/admin/master-data/events` must stay under the admin path and remain authentication-protected.

### 2) Auth Agent

Responsible for OIDC-related logic.

- Change scope: `internal/auth`, authentication middleware.
- Must preserve:
  - Do not introduce local username/password login logic.
  - Do not hardcode OIDC keys in code.
  - Control issuer/audience validation strategy through configuration.

### 3) Data Agent

Responsible for database connections, dialect compatibility, and the data access layer.

- Change scope: `internal/config`, `internal/storage`, repository layer.
- Must preserve:
  - Default rule: `APP_ENV=development` uses SQLite; `APP_ENV in {test, production}` uses PostgreSQL.
  - If `DATABASE_DRIVER` is explicitly set to `sqlite` or `pgx`, that value takes precedence.
  - Do not break existing configuration names.
  - Store by-id data and order indexes in Redis; pagination order comes from the order index.
  - Keep field constraints stable for the separate `cards` basic-info and params endpoints.

### 4) Environment Agent

Responsible for compose files, scripts, and developer experience.

- Change scope: `deploy/compose`, `scripts`, `Makefile`, `mise.toml`, `.mise/tasks`.
- Must preserve:
  - Support running test dependencies through the host container engine using Docker API and Docker Compose semantics.
  - Commands are idempotent and recoverable when run repeatedly.

## Execution Protocol

1. Read affected files before editing to avoid overwriting user changes.
2. Prefer small, focused changes. Do not perform unrelated refactors.
3. After each change, run at least:
   - If the change touches Go-related files such as `.go`, `go.mod`, or `go.sum`, prefer `go test ./...`.
   - If `go test ./...` is needed but the current environment has no `go` command, prefer running tests in a Docker container with cache directories mounted to avoid downloading dependencies every time.
   - When running Go commands in containers, prefer reusing Go module cache and build cache, for example by mounting `GOMODCACHE`, `GOCACHE`, or equivalent cache volumes.
   - If the change does not touch Go-related files, `go test ./...` is not required; if skipped, state that in the delivery note.
   - If the current environment cannot satisfy runtime requirements, `go test ./...` is not required; if skipped, state that in the delivery note.
   - If the change affects database schema, add a migration file. Do not run DDL directly in business code.
4. If the change touches compose files or scripts, also check:
  - `mise run dev-env-up`
  - `mise run dev-env-down`
5. For failures, fix only issues directly related to the current task.
6. All git commit messages must use Conventional Commits style, for example `feat(auth): add admin claim rbac` or `fix(sync): recover interrupted startup sync`.

## Security & Compliance Rules

- Do not commit real secrets, passwords, or tokens.
- Example credentials are for local development only and must be clearly documented.
- Do not log complete Bearer Tokens.

## Definition of Done

The task is done only when these conditions are met:

- If the change touches Go-related files, prefer running and passing `go test ./...`.
- If local `go` is unavailable, prefer completing `go test ./...` through a Docker container with cache directories mounted.
- If the change does not touch Go-related files, `go test ./...` is not a completion requirement, but state that in the delivery note.
- If the current environment cannot satisfy runtime requirements, `go test ./...` is not a completion requirement, but state that in the delivery note.
- Documentation, including README and env examples, matches actual behavior.
- New configuration keys are added to `.env.example`.
- The change scope is focused and easy to roll back.

## Preferred Commands

- Run API: `mise run run`
- Unit tests: `mise run test`
- Migrate up: `mise run migrate-up`
- Migrate down: `mise run migrate-down`
- Start dependencies: `mise run dev-env-up`
- Stop dependencies: `mise run dev-env-down`

## Cross-Repository Integration (sekai-master-api → sekai-viewer-reborn)

When you change sekai-master-api code (routes, handlers, response types) that affects
the API contract, the sekai-viewer-reborn SDK (`@platform/sekai-master-api-sdk`) must
be regenerated. The full workflow is:

1. Make changes in sekai-master-api.
2. Regenerate Swagger/OpenAPI spec: `mise run swagger`.
3. Restart the sekai-master-api dev server: `mise run dev`.
   Wait for the server to be ready before proceeding.
4. In sekai-viewer-reborn, regenerate the SDK:
   `mise run update-sekai-master-api-sdk-local`
   (This calls `http://localhost:18080/docs/openapi.json` by default.)
5. Validate: `pnpm --filter @platform/sekai-master-api-sdk check` in sekai-viewer-reborn.
6. Update any apps that consume the new endpoint (e.g. `@apps/content-site`).

The SDK generator overwrites generated artifacts (`src/sdk.gen.ts`, `src/types.gen.ts`,
`src/index.ts`). Always verify the generated output matches expectations before committing.
