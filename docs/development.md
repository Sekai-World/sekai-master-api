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

This starts PostgreSQL, Redis, Keycloak, Prometheus, and a metrics-only Alloy collector. It enables OTEL metrics export from the app without Loki or Tempo.

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

## Smoke Check

```sh
mise run smoke
```

`mise run smoke` requires `ADMIN_BEARER_TOKEN` for protected endpoint checks.
