# Master Data

## Sync

At startup, the API can sync parsed game database JSON files from one or more GitHub repositories into the PostgreSQL store. Startup sync runs in the background after the HTTP listener is available.

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
- Commit comparison uses the GitHub REST API first and falls back to Git smart HTTP ref advertisement when the REST lookup is unavailable, so anonymous API rate limits do not force every region into a full archive sync. The fallback resolves only advertised branches, tags, or `HEAD`; a raw SHA configured as `MASTER_DATA_GITHUB_REF_<REGION>` cannot be resolved through this fallback, so operators relying on it must configure a branch or tag instead.
- For a normal sync after resolving a distinct remote commit, the upstream-complete `versions.json` manifest is fetched at that exact commit. Because the upstream contract includes every API-relevant synchronized data change, a manifest equal to the stored version payload allows the archive load to be skipped while the store still holds the region; `SourceCommit` remains pinned to the resolved commit as the local snapshot identity.
- Unchanged regions skip the load when the store still holds the region (`HasRegionData`) and its version payload; relation indexes and list projections whose definitions changed are rebuilt from the stored records (`EnsureDerivedEntityData`). Otherwise the region falls back to a full sync.
- Admin dashboard status items are read-only views of persisted sync status. They do not downgrade a successful sync to `pending` just because the current process has not retained a decoded runtime cache/index after restart.
- Available-region reads, dashboard status, readiness probes, and observability metric callbacks only read sync status and the store; only sync writes master data.
- The `serve` role's Kubernetes `/readyz` probe is a deeper, bounded, read-only readiness snapshot for split deployment: it verifies PostgreSQL, then requires every configured region to have persisted card records **and** a complete `versions.json` payload (the same contract the public `/versions` response enforces) before reporting the pod ready. Its response enumerates affected (unready) regions and never includes secrets such as database URLs or source repository references. A store read error reports the reason `database`.
- Changed regions download one GitHub tarball for the resolved commit and extract JSON files under the configured path.
- Store writes are per entity: an entity whose source digest and derived-data versions match is skipped, and a changed entity is rewritten in one transaction (see [PostgreSQL Store](#postgresql-store)).
- A changed region's whole extracted payload stays in memory from extraction through the store (about 290 MB of compact JSON records for JP at 6.8.0). Blocks and projections are zstd-compressed by pooled single-concurrency encoders, and reads share one decoder.
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

- `tmp/master-data-sync-resume/` (overridable via `MASTER_DATA_RESUME_BASE_DIR`)

There is no local backup: the source repositories on GitHub are the canonical
copy of every record, and a forced sync rewrites a region.

## PostgreSQL Store

Master data lives in PostgreSQL (`storage.PostgresMasterDataStore`; design in
[postgres-master-data-store.md](postgres-master-data-store.md)). The Redis
store was removed: the process does not connect to Redis, and
`MASTER_DATA_STORE` is optional and accepts only `postgres`.

- **Tables.** `master_entities` (one row per region and entity: revision,
  tagged source digest, record count, zstd order keys, index and projection
  versions), `master_blocks`, `master_record_index`, `master_projections`, and
  `master_versions`.
- **Blocks.** Records are sorted by `masterdata.BlockSortKey` and stored in
  blocks that close at 32 record keys or on the record that brings them to
  64 kB of JSON. A block is a zstd JSON array of
  `[record_key, [position, ...], record]`: a key the source repeats keeps
  every position and reads the record stored last under it.
- **Writes.** Each entity is one transaction: it rewrites the blocks and order
  keys when the revision changes, the postings and projection when the records
  or their definitions change, and then the entity row. An entity whose source
  digest, index version, and projection version all match is skipped without
  parsing. A write that carries the sync lease's token (sync jobs do) first
  locks the lease row `FOR SHARE` and fails with `ErrFencedOut` unless the
  token is still current; writes without a token (lease disabled) pass.
  After a full region load, entities the source no longer has are deleted.
- **Reads.** Every read that must see one version of an entity is one
  statement: `GetByID`, `GetByIDs`, and `GetByCompositeKeys` probe the block
  with the greatest `first_key` at or below each key; `ListAll` reads all
  blocks; `ListByIndex` reads the index version, the postings, and their
  blocks together, using the SQL `master_block_sort_key()` function.
  `ListByPage` reads the order keys and then the page. Batch reads decode
  records on every core, so handlers batch keys into one call rather than
  looping over `GetByID`.
- **Readiness.** A region counts as populated when an entity has records
  (`HasRegionData`), which the commit-unchanged shortcut and the cache-ready
  check use.
- **Contract.** `internal/storage/master_data_store_contract_test.go` pins the
  answers of the reads handlers and sync use.
- **Inspecting records.** Blocks are opaque to SQL; use
  `sekai-master-api dump` (see
  [Inspecting records](postgres-master-data-store.md#inspecting-records)).

## Keys, Indexes, and Projections

`resourceboxes`, `resourceboxdetails`, and `charactermissionv2parametergroups` are keyed by composite keys (`masterdata.CompositeKeyFields`) instead of bare business IDs: resource boxes key on `(id, resourceBoxPurpose)`, details key on `(resourceBoxId, resourceBoxPurpose, seq)`, and parameter groups key on `(id, seq)`. The original record body and business fields are unchanged. Records missing any required key component receive deterministic `auto:` keys, with an occurrence suffix for identical incomplete records so they remain independently listable. Read a record whose full key is known with `GetByCompositeKeys` (any number of keys in one read, for example `{id, resourceBoxPurpose: "event_ranking_reward"}`); `GetByID` and `GetByIDs` intentionally report no match for these entities because a bare ID cannot disambiguate them.

Relation indexes answer "which records have this field value" without decoding an entity. `masterdata.EntityIndexes` (`internal/domain/masterdata/index.go`) defines them per entity, for example `gachas` by `gachaPickups.cardId`, `resourceboxes` by `id`, `resourceboxdetails` by `resourceBoxId,resourceBoxPurpose`, and the event, card, music, and virtual live child tables by their parent ID. A field path steps into objects with `.` and indexes every element of an array; several fields are joined with `,`. Each index maps the canonical field value (decimal numbers, trimmed strings, length-prefixed parts for several fields) to the storage keys carrying it, in stored order, as postings in `master_record_index`. Sync builds the indexes from the records it stores and writes them in the same transaction as the records, so readers never see an index that disagrees with the data. The entity's `index_version` records the definitions they were built from: a store rebuilds an entity's indexes when its records change or its definitions differ, even for an unchanged source file, and a sync that skips an unchanged commit calls `EnsureDerivedEntityData` to build out-of-date indexes and list projections from the stored records, falling back to a full sync if that fails. `ListByIndex` answers any number of lookups with one index read and one batched record read; until an entity's index is built it scans the entity and logs `master data index not built; scanning entity`, so results stay correct right after a deploy.

Handlers read a request's related records through `GetByIDs`, `GetByCompositeKeys`, or `ListByIndex` and must not decode a large entity with `ListAll` per request or per item; the API latency budget is under 1 s per request and never over 3 s. Mission and character rank reward catalogs are built per request from the boxes, details, and items the request references. List endpoints read list projections instead of entities. `masterdata.ProjectionFields` (`internal/domain/masterdata/projection.go`) lists, per entity, the fields its list filters, sorts, and spoiler-checks on, and for small lists everything they return (currently `cards`, `gachas`, `costume3ds`, and `costume3dgroups`, plus `events` with the window fields the current-event lookup compares). Sync builds each projection from the records it stores, column by column in the narrowest type that holds every value (int, float, string, bool, or raw JSON for mixed fields), gob-encodes and zstd-compresses it into `master_projections`, and writes it with its projection version in the same transaction as the records. `LoadProjection` returns it; a region without the entity gets an empty projection, and until sync has written one it is built from the entity's records with a `master data projection not built` warning. Small lists turn projection rows into records and reuse the shared filter and sort helpers; the 3D costume list (about 124k JP rows) deduplicates, filters, and sorts projection rows directly and normalizes only the requested page. No derived data is cached in process. Event, music, and virtual live lists and their cross-entity filters still read full entities until they move to projections. Card and music lists and event rewards read their related records with one `GetByIDs` per entity through `shared.PrefetchRecords`.

Query behavior:

- Card by-id reads one stored record.
- Card metadata batch reads perform direct persisted by-id lookups only, return `id`, `prefix`, `assetbundleName`, `attr`, and `rarityType`, and omit missing cards.
- Card list pagination follows real `cards.json` array order, not contiguous IDs.
- Card params reuses the stored card record and returns params-related fields only.
- Music responses expand `creatorArtistId` and `liveStageId` and hide the raw ids.
- Card responses expand `cardSupplyId`, `skillId`, `characterId`, and `cardRarityType`. Card rarity enrichment lists persisted `cardrarities` records directly.
- Event card and music relation endpoints preserve relation fields and enrich minimal display fields from `cards`/`musics`; event music `seq` remains the relation sequence, not the master music sequence.
- `GET /api/v1/events/{region}/{id}/detail` is a bounded first-screen aggregate: it returns event detail, availability/current metadata, bonuses, enriched cards/musics, and reward preview/summary. It intentionally does not include every ranking reward range; use `/events/{region}/{id}/rewards` for the full reward payload.
- Event current lookup picks the in-window event with the latest start from the `events` list projection and reads that one record; it never writes.
- Event by-id omits `eventRankingRewardRanges`; use the rewards endpoint.
- Gacha list accepts the optional `ongoing` boolean. When `ongoing=true`, only records with `startAt <= now <= endAt` are returned, with filtering applied before sorting and pagination; omitted or `false` preserves the existing list behavior.
- Virtual live base response omits items, schedules, and setlists; use dedicated endpoints.
- If a top-level `releaseConditionId` exists, the response expands `releaseCondition` and hides `releaseConditionId`. Missing related records produce `null`; storage failures on required music/card enrichment paths return the endpoint's existing query error instead of an incomplete `200` response.
- Region data endpoints return `503 REGION_DATA_NOT_READY` until the region's current persisted sync status is `success` and the store holds records of the entity the endpoint reads (`shared.EnsureRegionReadyForEntityRecords`), so they cannot expose in-progress or failed sync contents.
- Availability endpoints list the regions whose latest sync succeeded and that hold the requested record.
