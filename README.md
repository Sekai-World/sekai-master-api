# sekai-master-api

[![CI](https://github.com/Sekai-World/sekai-master-api/actions/workflows/ci.yml/badge.svg)](https://github.com/Sekai-World/sekai-master-api/actions/workflows/ci.yml)
[![Swagger Check](https://github.com/Sekai-World/sekai-master-api/actions/workflows/swagger-check.yml/badge.svg)](https://github.com/Sekai-World/sekai-master-api/actions/workflows/swagger-check.yml)

Golang RESTful API for Sekai master data, built with Gin, OIDC bearer-token validation, Goose migrations, Redis cache, and environment-based database selection.

## Features

- Public master-data GET APIs under `/api/v1`
- Public versions endpoints: `GET /api/v1/versions` and `GET /api/v1/versions/:region`
- Public lookup endpoints: `GET /api/v1/unitProfiles/:region/:unit`, `GET /api/v1/gameCharacterUnits/:region/:id`, and `GET /api/v1/gameCharacters/:region/:id`
- Public card metadata batch endpoint: `GET /api/v1/cards/:region/batch?ids=1,2,3`
- OIDC-protected admin APIs and dashboard
- SQLite for development; PostgreSQL for test and production
- Redis-backed master-data cache with specialized card/music/event/virtual-live queries
- Multi-region GitHub master-data sync
- Local Docker Compose core stack for PostgreSQL, Redis, and Keycloak, with opt-in Prometheus/Grafana/Loki/Tempo/Alloy observability modes
- Swagger UI in development and test environments

## Quick Start

```sh
cp .env.example .env
mise trust
mise install
mise run tidy
mise run run
```

For the default lightweight local dependency stack:

```sh
mise run dev-env-up
mise run dev
```

For local metrics only:

```sh
mise run dev-env-up-metrics
mise run dev-metrics
```

For the full local observability stack:

```sh
mise run dev-env-up-full
mise run dev-full
```

## Common Commands

- `mise run run`: run the API on the host
- `mise run test`: run tests
- `mise run lint`: run formatting check and `go vet`
- `mise run format`: format Go files
- `mise run swagger`: regenerate Swagger docs
- `mise run dev-env-up`: start local dependencies
- `mise run dev-env-up-metrics`: start local dependencies plus Prometheus and metrics-only Alloy
- `mise run dev-env-up-full`: start local dependencies plus Loki, Tempo, Prometheus, Alloy, and Grafana
- `mise run dev-env-down`: stop local dependencies
- `mise run migrate-up`: run migrations up
- `mise run migrate-down`: run migrations down

## Documentation

- [Development](docs/development.md)
- [API Reference](docs/api.md)
- [Master Data](docs/master-data.md)
- [Auth and Admin](docs/auth-admin.md)
- [Production Observability on K3s](docs/production-observability-k3s.md)

## Swagger

Swagger UI is exposed only in `development` and `test`:

- `GET /docs/index.html`
- `GET /docs/openapi.json`

Generated Swagger files live in `internal/transport/http/swaggerdocs`; root `docs/` is reserved for project documents.
