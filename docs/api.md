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
- `GET /api/v1/unitProfiles/:region/:unit/members`
- `GET /api/v1/gameCharacterUnits/regions/:id/availability`
- `GET /api/v1/gameCharacterUnits/:region/list?page=1&page_size=20`
- `GET /api/v1/gameCharacterUnits/:region/:id`
- `GET /api/v1/gameCharacters/regions/:id/availability`
- `GET /api/v1/gameCharacters/:region/list?page=1&page_size=20`
- `GET /api/v1/gameCharacters/:region/:id`
- `GET /api/v1/mysekaiPhotoDecorations/:region/list?page=1&page_size=20`
- `GET /api/v1/mysekaiPhotoDecorations/:region/:id`
- `GET /api/v1/honors/:region/list?page=1&page_size=20`
- `GET /api/v1/honors/:region/:id`
- `GET /api/v1/bondsHonors/:region/list?page=1&page_size=20&game_character_ids=1,2`
- `GET /api/v1/bondsHonors/:region/:id`
- `GET /api/v1/costume3ds/:region/list?page=1&page_size=20`
- `GET /api/v1/costume3ds/:region/:id`
- `GET /api/v1/missions/:region/list?family=storyMissions|characterMissionV2s|normalMissions&page=1&page_size=20`
- `GET /api/v1/missions/:region/:family/:id`
- `GET /api/v1/cards/:region/list?page=1&page_size=20`
- `GET /api/v1/cards/:region/batch?ids=1,2,3` (up to 100 positive integer IDs; missing cards are omitted and items preserve first-seen request order)
- `GET /api/v1/cards/:region/:id`
- `GET /api/v1/cards/:region/:id/params`
- `GET /api/v1/cards/:region/:id/episodes`
- `GET /api/v1/musics/:region/list?page=1&page_size=20`
- `GET /api/v1/musics/:region/:id/difficulties`
- `GET /api/v1/musics/:region/:id`
- `GET /api/v1/gachas/:region/list?page=1&page_size=20&ongoing=true&sort_by=id|startAt&sort_order=asc|desc`
- `GET /api/v1/events/:region/current`
- `GET /api/v1/events/:region/list?page=1&page_size=20&id=<id>&name=<kw>&unit=<kw>&event_type=<kw>&sort_by=id|startAt&sort_order=asc|desc`
- `GET /api/v1/events/:region/:id`
- `GET /api/v1/events/:region/:id/detail`
- `GET /api/v1/events/:region/:id/rewards`
- `GET /api/v1/events/:region/:id/honor-bonuses`
- `GET /api/v1/virtualLives/:region/list?page=1&page_size=20&id=<id>&name=<kw>&virtual_live_type=<kw>&sort_by=id|startAt&sort_order=asc|desc`
- `GET /api/v1/virtualLives/:region/:id`
- `GET /api/v1/virtualLives/:region/:id/items`
- `GET /api/v1/virtualLives/:region/:id/schedules`
- `GET /api/v1/virtualLives/:region/:id/setlists`

List endpoints hide spoiler content by default. Pass `spoiler=true` to include records with a future `releaseAt` or `startAt`.

Generic master-data lookup lists and the `mysekaiPhotoDecorations`, `honors`, and `bondsHonors` lists accept a positive `page` and a `page_size` from 1 to 100 (default 20). Requests with `page_size` greater than 100 return `400 INVALID_REQUEST`.

MySekai photo decoration responses expose only `id`, `seq`, `name`, `description`, and `assetbundleName`. Honor responses expose the stable honor fields, normalized `levels`, and an optional same-region `group` looked up from `honorgroups`; missing groups are omitted, and no cross-region fallback is used. Honor list group lookups are deduplicated within the current page. Unknown upstream fields are not returned.

Bonds honor responses expose only `id`, `seq`, `bondsGroupId`, `gameCharacterUnitId1`, `gameCharacterUnitId2`, `honorRarity`, `name`, `pronunciation`, `description`, `configurableUnitVirtualSinger`, normalized `levels` (`id`, `bondsHonorId`, `level`, and `description`), and same-region associations; missing fields are omitted. The optional `bondsGroup` exposes only `groupId`, `characterId1`, and `characterId2`, matched by `bondsHonors.bondsGroupId` to `bonds.groupId` (not `bonds.id`). `characterUnit1`/`characterUnit2` expose only `id`, `gameCharacterId`, and `unit`, matched to `gamecharacterunits.id`. Missing associations are omitted. Lists default to `seq ASC` with `id ASC` tie-breaking, support exact positive `bonds_group_id`, `game_character_unit_id1`, and `game_character_unit_id2` filters, and paginate only after projection, filtering, and sorting. The optional `game_character_ids` filter accepts exactly two different positive underlying game character IDs, resolves them through each honor's two `gamecharacterunits` associations, and matches the pair without order sensitivity; records with either unresolved unit association do not match. It combines with the existing filters using AND. `sort_by` accepts `id`, `seq`, `bondsGroupId`, `gameCharacterUnitId1`, `gameCharacterUnitId2`, `honorRarity`, or `name`; `sort_order` accepts `asc` or `desc`. Unknown or malformed query parameters return `400 INVALID_REQUEST`.

Costume3D endpoints normalize embedded costume/group fields and fill only missing fields from `costume3dgroups` in the same region; a missing group does not remove the costume. Responses expose only typed stable fields (`id`, `groupId`, `colorId`, `partType`, `seq`, `name`, `designer`, `characterId`, `rarity`, `type`, `assetbundleName`, and `publishedAt`, in epoch milliseconds). Costume3D lists accept `page_size` from 1 to 100 (default 20).

Mission endpoints allow only the explicit `storyMissions`, `characterMissionV2s`, and `normalMissions` families. They use typed family-specific projections and omit unknown upstream fields. Lists support positive `id` and `event_id` filters; `character_id` is supported only for `characterMissionV2s`. `sort_by` and `sort_order=asc|desc` are validated per family, and pagination is applied after normalization. Reward references are typed and keep `status: unresolved` when a resource-box purpose or unique same-region relationship cannot be established. Resource-box IDs are shared across purposes, so a reference without its own purpose prefers the family's purpose: story missions (which store only a top-level `resourceBoxId`) use `story_mission`, and reward entries whose `missionType` ends in `_mission` use `mission_reward`; when no box of that purpose matches, the ID-only match still applies. Resource boxes and details are batch-loaded with `ListAll` only, and the decoded indexes are reused per process while the entity revision is unchanged (see [Master Data](master-data.md#cache-strategy)); no cross-region fallback or by-id lookup is used.

The unit profile members endpoint resolves the unit profile using the trimmed, normalized `unit` value and returns an `items` envelope. Each usable member includes the membership `id`, `gameCharacterId`, `unit`, `colorCode`, and joined character fields when present: `firstName`, `givenName`, `firstNameEnglish`, `givenNameEnglish`, and `resourceId`. Items are ordered by ascending numeric `gameCharacterId`. Membership records whose referenced game character is missing are excluded.

Event list filters are optional and matched together. `id` is an exact match; `name`, `unit`, and `event_type` are case-insensitive partial matches. `unit` and `event_type` accept comma-separated multiple values, and `unit` is matched against `eventStoryUnits.unit`.

Virtual Live list filters are optional and matched together. `id` is an exact numeric match; `name` is a case-insensitive (trimmed) substring match; `virtual_live_type` is a comma-separated list of virtual live types combined with OR within the parameter and AND combined with other filters. `sort_by` accepts `id` or `startAt`; `sort_order` accepts `asc` or `desc`. The dedicated detail sub-resources (`/items`, `/schedules`, `/setlists`) and `/regions/:id/availability` are also available for virtual lives.

Gacha list requests support the optional `ongoing` boolean. When `ongoing=true`, records are filtered to `startAt <= now <= endAt` before sorting and pagination; omitted or `false` preserves the existing list behavior.

Event honor bonuses are returned as a typed `items` envelope filtered to the requested event and sorted by ascending `id`. Each item exposes only `id`, `eventId`, `honorId`, `leaderGameCharacterId`, and `bonusRate`, with same-region lightweight `honor`, `honor.group`, and `leaderGameCharacterUnit` associations when present. This endpoint accepts no query parameters.

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
