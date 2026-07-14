# Master Data

## Sync

At startup, the API can sync parsed game database JSON files from one or more GitHub repositories into cache. Startup sync runs in the background after the HTTP listener is available.

Region sources are configured with:

- `MASTER_DATA_REGIONS=jp,global`
- `MASTER_DATA_GITHUB_OWNER_<REGION>`
- `MASTER_DATA_GITHUB_REPO_<REGION>`
- `MASTER_DATA_GITHUB_REF_<REGION>`
- `MASTER_DATA_GITHUB_PATH_<REGION>`

`<REGION>` is uppercase with non-alphanumeric characters replaced by `_`.

Example:

```env
MASTER_DATA_GITHUB_OWNER_JP=Sekai-World
MASTER_DATA_GITHUB_REPO_JP=sekai-master-data-jp
MASTER_DATA_GITHUB_REF_JP=main
MASTER_DATA_GITHUB_PATH_JP=data
```

Set `MASTER_DATA_GITHUB_TOKEN` if higher GitHub API rate limits are needed.

## Sync Behavior

- Startup sync compares the configured source commit with the latest successful sync record.
- Unchanged regions validate persisted Redis search indexes and rebuild missing or stale persisted indexes when needed.
- Admin dashboard status items are read-only views of persisted sync status. They do not downgrade a successful sync to `pending` just because the current process has not retained a decoded runtime cache/index after restart.
- Available-region reads, dashboard readiness region lists, and observability metric callbacks are read-only runtime readiness snapshots. They report whether the current process has retained usable runtime cache/index state, but they do not load, rebuild, rewrite, or repair Redis search indexes.
- Safe single-entity data endpoints can still serve from persisted Redis by-id records after a process restart even when decoded search indexes have not been warmed in memory, but only while the region's current persisted sync status is `success`.
- If Redis data is missing but local backup exists, cache is restored from backup.
- Changed regions download one GitHub tarball for the resolved commit and extract JSON files under the configured path.
- Cache writes are incremental: changed records are upserted and deleted records are removed.
- Sync status is persisted in `master_data_sync_status`; latest status is exposed through `master_data_sync_status_latest`.
- Sync status includes region, state, file count, source info, source commit, sync duration, and timestamps.
- Sync events are exposed through `GET /api/v1/admin/master-data/events`.
- GitHub webhooks require `MASTER_DATA_GITHUB_WEBHOOK_SECRET` and a valid `X-Hub-Signature-256` header. If the secret is empty, the webhook endpoint returns `503 GITHUB_WEBHOOK_DISABLED`.

Useful settings:

- `MASTER_DATA_RECOVER_INTERRUPTED_SYNC`
- `MASTER_DATA_SYNC_CONCURRENCY`
- `MASTER_DATA_HTTP_RETRY_COUNT`
- `MASTER_DATA_HTTP_RETRY_BACKOFF_MS`
- `MASTER_DATA_GITHUB_WEBHOOK_SECRET`
- `MASTER_DATA_WARM_SEARCH_INDEXES` controls optional startup persisted search-index warmup. It defaults to off in development so indexes are ensured lazily per searched entity, and defaults to on outside development unless explicitly overridden.
- `MASTER_DATA_SEARCH_INDEX_CACHE_ENTRIES` bounds the in-process LRU cache of decoded search indexes. The default is `32`; set it to `0` to disable decoded-index retention while keeping Redis persisted indexes authoritative.

Temporary sync workspace:

- `tmp/master-data-sync-resume/`
- `tmp/master-data-backup/<region>/latest/`

## Cache Strategy

Redis settings:

- `REDIS_ADDR`
- `REDIS_PASSWORD`
- `REDIS_DB`
- `MASTER_DATA_REDIS_KEY_PREFIX`

Search indexes are scoped to fields used by API search paths instead of every scalar field. The default searchable field is `name`; `cards` additionally indexes `prefix` and `cardSkillName`; relationship lookups index `cardId`, `eventId`, `musicId`, `virtualLiveId`, `eventStoryId`, `cardRarityType`, and `unit` when those fields are present. Persisted search indexes live in Redis as entity payload keys plus matching `:search-index-version` keys. Decoded Go in-memory index structures are held only in the bounded `MASTER_DATA_SEARCH_INDEX_CACHE_ENTRIES` LRU cache, so hot searched entities avoid repeated Redis decode work without retaining every region/entity forever. Loading an older persisted search index filters out fields outside that policy before using it in memory. Older Redis payloads may also use the legacy raw `{field: [id, ...]}` shape instead of `{id, normalized_text}` items; load-time migration resolves those IDs through the entity `:by-id` hash, rebuilds the searchable text, and rewrites the compact persisted index format.

`MASTER_DATA_WARM_SEARCH_INDEXES` and the related ensure flow validate that the persisted Redis search indexes needed by search endpoints already exist, or rebuild those persisted indexes when Redis is missing or stale data. Search misses may also rebuild the requested entity index from Redis by-id data. Any decoded indexes created during warmup, ensure, or search are still subject to the same bounded LRU cache; Redis remains the authoritative source. Rebuild cleanup deletes stale search-index payload keys, their matching version keys, and empty region search-index entity sets so Redis key counts do not retain orphaned index metadata.

Query behavior:

- Card by-id reads from Redis hash cache.
- Card metadata batch reads perform direct persisted by-id lookups only, return `id`, `prefix`, `assetbundleName`, `attr`, and `rarityType`, and omit missing cards.
- Card list pagination follows real `cards.json` array order, not contiguous IDs.
- Card params reuses the cached card record and returns params-related fields only.
- Music responses expand `creatorArtistId` and `liveStageId` and hide the raw ids.
- Card responses expand `cardSupplyId`, `skillId`, `characterId`, and `cardRarityType`. Card rarity enrichment lists persisted `cardrarities` records directly rather than invoking search-index repair.
- Event card and music relation endpoints preserve relation fields and enrich minimal display fields from `cards`/`musics`; event music `seq` remains the relation sequence, not the master music sequence.
- `GET /api/v1/events/{region}/{id}/detail` is a bounded first-screen aggregate: it returns event detail, availability/current metadata, bonuses, enriched cards/musics, and reward preview/summary. It intentionally does not include every ranking reward range; use `/events/{region}/{id}/rewards` for the full reward payload.
- Event current lookup uses Redis first and refreshes from `events.json` when stale or missing.
- Event by-id omits `eventRankingRewardRanges`; use the rewards endpoint.
- Virtual live base response omits items, schedules, and setlists; use dedicated endpoints.
- If a top-level `releaseConditionId` exists, the response expands `releaseCondition` and hides `releaseConditionId`. Missing related records produce `null`; storage failures on required music/card enrichment paths return the endpoint's existing query error instead of an incomplete `200` response.
- Region data endpoints return `503 REGION_DATA_NOT_READY` until the required persisted data is available or the current process reports usable read-only runtime cache/index state for that region. Safe single-entity list/basic endpoints require both a current persisted `success` sync status and a non-empty entity `:by-id` hash checked with read-only status/Redis probes before falling back to strict runtime readiness, so these reads do not depend on decoded search-index LRU state after restart and cannot expose in-progress or failed sync contents.
- The current relaxed set is intentionally narrow: `cards` by-id/params/list, `gachas` by-id/list, `musics` by-id/list, `events` list/current, `unitProfiles` by-unit/list, `gameCharacterUnits` by-id/list, and `gameCharacters` by-id/list.
- Composite, enriched, search-backed, availability, available-region, dashboard readiness region lists, metrics, and other multi-entity endpoints still require strict runtime readiness. Dashboard status items are the exception: they report the persisted sync status instead of runtime cache readiness. For `musics`, this means detail, difficulties, vocals, and availability remain strict even though the basic by-id/list endpoints can serve from persisted `musics` records. For `events`, detail, by-id detail expansion, break-times, rewards, bonus/composite, availability, and search-like endpoints remain strict even though list/current can serve from persisted `events` records. Run the explicit sync, warmup, or ensure flow to repair persisted Redis indexes instead of relying on status/availability read endpoints to do it.
