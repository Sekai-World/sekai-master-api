# API Reference

All public API routes use the `/api/v1` prefix. Non-admin GET APIs are public by default.

Swagger UI is available only in `development` and `test`:

- `GET /docs/index.html`
- `GET /docs/openapi.json`

## Public Endpoints

- `GET /api/v1/health`
- `GET /api/v1/versions`
- `GET /api/v1/versions/:region`
- `GET /api/v1/unitProfiles/regions/:unit/availability`
- `GET /api/v1/unitProfiles/:region/list?page=1&page_size=20`
- `GET /api/v1/unitProfiles/:region/:unit`
- `GET /api/v1/gameCharacterUnits/regions/:id/availability`
- `GET /api/v1/gameCharacterUnits/:region/list?page=1&page_size=20`
- `GET /api/v1/gameCharacterUnits/:region/:id`
- `GET /api/v1/gameCharacters/regions/:id/availability`
- `GET /api/v1/gameCharacters/:region/list?page=1&page_size=20`
- `GET /api/v1/gameCharacters/:region/:id`
- `GET /api/v1/cards/:region/list?page=1&page_size=20`
- `GET /api/v1/cards/:region/:id`
- `GET /api/v1/cards/:region/:id/params`
- `GET /api/v1/cards/:region/:id/episodes`
- `GET /api/v1/musics/:region/list?page=1&page_size=20`
- `GET /api/v1/musics/:region/:id`
- `GET /api/v1/events/:region/current`
- `GET /api/v1/events/:region/list?page=1&page_size=20&id=<id>&name=<kw>&unit=<kw>&event_type=<kw>&sort_by=id|startAt&sort_order=asc|desc`
- `GET /api/v1/events/:region/:id`
- `GET /api/v1/events/:region/:id/rewards`
- `GET /api/v1/virtualLives/:region/list?page=1&page_size=20`
- `GET /api/v1/virtualLives/:region/:id`
- `GET /api/v1/virtualLives/:region/:id/items`
- `GET /api/v1/virtualLives/:region/:id/schedules`
- `GET /api/v1/virtualLives/:region/:id/setlists`

Event list filters are optional and matched together. `id` is an exact match; `name`, `unit`, and `event_type` are case-insensitive partial matches. The `unit` filter is matched against `eventStoryUnits.unit`.

## Admin Endpoints

Bearer token from the configured OIDC provider is required.

- `GET /api/v1/admin/profile`
- `GET /api/v1/admin/master-data/events`
- `GET /api/v1/admin/master-data/status`
- `POST /api/v1/admin/master-data/sync`
- `POST /api/v1/admin/master-data/sync/force`

The SSE endpoint `GET /api/v1/admin/master-data/events` is under the admin path and remains authentication-protected. The dashboard SSE connection may pass `access_token` as a query parameter.

Sync endpoints accept optional JSON:

```json
{ "region": "jp" }
```

Use an empty payload for all configured regions.

## Internal Endpoints

- `POST /api/v1/internal/github/webhooks/master-data`

This endpoint requires `MASTER_DATA_GITHUB_WEBHOOK_SECRET` and a valid `X-Hub-Signature-256` header. It matches configured GitHub source `owner`, `repo`, and `ref`, and only push events with changed `versions.json` trigger sync.
