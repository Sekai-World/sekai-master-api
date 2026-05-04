# API Reference

All public API routes use the `/api/v1` prefix. Non-admin GET APIs are public by default.

Swagger UI is available only in `development` and `test`:

- `GET /docs/index.html`
- `GET /docs/doc.json`

## Public Endpoints

- `GET /api/v1/health`
- `GET /api/v1/cards/:region/list?page=1&page_size=20`
- `GET /api/v1/cards/:region/search?q=<keyword>&field=name|skill&page=1&limit=20`
- `GET /api/v1/cards/:region/:id`
- `GET /api/v1/cards/:region/:id/params`
- `GET /api/v1/cards/:region/:id/episodes`
- `GET /api/v1/musics/:region/list?page=1&page_size=20`
- `GET /api/v1/musics/:region/search?title=<kw>&lyricist=<kw>&composer=<kw>&arranger=<kw>&page=1&limit=20`
- `GET /api/v1/musics/:region/:id`
- `GET /api/v1/events/:region/current`
- `GET /api/v1/events/:region/:id`
- `GET /api/v1/events/:region/:id/rewards`
- `GET /api/v1/virtualLives/:region/list?page=1&page_size=20`
- `GET /api/v1/virtualLives/:region/search?q=<keyword>&field=name|type|assetbundle&page=1&limit=20`
- `GET /api/v1/virtualLives/:region/:id`
- `GET /api/v1/virtualLives/:region/:id/items`
- `GET /api/v1/virtualLives/:region/:id/schedules`
- `GET /api/v1/virtualLives/:region/:id/setlists`

Music search requires at least one of `title`, `lyricist`, `composer`, or `arranger`. Multiple fields are matched together.

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

This endpoint matches configured GitHub source `owner`, `repo`, and `ref`, and only push events with changed `versions.json` trigger sync.
