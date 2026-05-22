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
- Unchanged regions rebuild the in-memory index from Redis when possible.
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

Temporary sync workspace:

- `tmp/master-data-sync-resume/`
- `tmp/master-data-backup/<region>/latest/`

## Cache Strategy

Redis settings:

- `REDIS_ADDR`
- `REDIS_PASSWORD`
- `REDIS_DB`
- `MASTER_DATA_REDIS_KEY_PREFIX`

Query behavior:

- Card by-id reads from Redis hash cache.
- Card list pagination follows real `cards.json` array order, not contiguous IDs.
- Card params reuses the cached card record and returns params-related fields only.
- Music responses expand `creatorArtistId` and `liveStageId` and hide the raw ids.
- Card responses expand `cardSupplyId`, `skillId`, `characterId`, and `cardRarityType`.
- Event current lookup uses Redis first and refreshes from `events.json` when stale or missing.
- Event by-id omits `eventRankingRewardRanges`; use the rewards endpoint.
- Virtual live base response omits items, schedules, and setlists; use dedicated endpoints.
- If a top-level `releaseConditionId` exists, the response expands `releaseCondition` and hides `releaseConditionId`.
- Region data endpoints return `503 REGION_DATA_NOT_READY` until region sync status is `success` and Redis has usable cache/index data for that region.
