# Copilot Instructions for `sekai-master-api`

## Tech Stack

- Language: Go
- HTTP Framework: Gin
- Auth: Keycloak (OIDC/JWT validation)
- Databases:
  - Default: development uses SQLite, test/production use PostgreSQL
  - Optional override: `DATABASE_DRIVER=sqlite|pgx`
- Local infra: Docker Compose semantics (on Podman API when using devcontainer)

## Repository Conventions

1. Keep handlers thin; move business logic to service/usecase layer.
2. Return consistent JSON error shape via shared response helpers.
3. Avoid introducing local authentication flows; rely on Keycloak tokens.
4. Only admin-related API routes should apply auth middleware; non-admin `GET` APIs must remain public.
5. Admin dashboard should provide its own login page and use Keycloak token validation.
6. Keep environment-driven behavior in `internal/config`.
7. Add new env vars to `.env.example` and document in `README.md`.

## Architecture Guardrails

- `cmd/api`: entrypoint only (wire dependencies, start server)
- `internal/transport/http`: routing, middleware, handlers
- `internal/auth`: token verification / auth utilities
- `internal/storage`: DB connection/bootstrap

Do not couple handlers directly to concrete database implementations when avoidable.

## Coding Rules

- Make minimal, focused changes.
- Do not rename/move files unless required by the task.
- Do not add dependencies without clear need.
- Follow existing naming and package boundaries.
- Do not add inline comments unless requested.

## Testing Expectations

After code changes, run:

```bash
go test ./...
```

If compose/dev environment is affected, also validate commands:

```bash
make dev-env-up
make dev-env-down
```

## Environment Notes (Windows + WSL2 + Podman)

- Devcontainer uses Docker CLI/Compose semantics against host Podman API.
- Keep `DOCKER_HOST`/socket usage configurable (do not hardcode host-specific paths in app code).
- Prefer reproducible commands in `Makefile` over ad-hoc shell snippets.

## Documentation Requirements

Any functional change must be reflected in:

- `README.md` (usage or behavior)
- `.env.example` (new/changed env vars)

Keep docs concise and aligned with real behavior.
