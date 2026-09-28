# PostgreSQL Master-Data Store — Design

Status: approved direction (2026-09-28). Decisions are recorded in
[Decisions](#decisions).

This document specifies how `sekai-master-api` moves master data from Redis,
which today is its only store, to PostgreSQL. Redis becomes an optional,
disposable cache. Records are stored as compressed, key-sorted blocks rather
than one row per record. This document is the design gate before
implementation.

## Problem

Redis is the system of record for master data, but it is configured and
operated as a cache:

- Both master-data Redis instances run with
  `--maxmemory 1536mb --maxmemory-policy allkeys-lru`
  (`sekai-k3s-infra/clusters/test/platform/redis-sekai-master*/redis.yaml`).
- On 2026-09-28 the dev instance reached 1.45 GB and had **evicted 3436 keys**
  (`INFO stats`), including JP `actionsets:by-id`. As a result,
  `/api/v1/actionSets/jp/list` returned 503 `REGION_DATA_NOT_READY`.
  Evictions are silent, and nothing restores the lost keys until the next
  full sync.
- Dev memory by key kind (5 regions, `MEMORY USAGE` over all 9974 keys):

  | Keys | Memory |
  |---|---|
  | records (`:by-id`) | 1280 MB |
  | search indexes (no HTTP consumer) | 129 MB |
  | order lists | 97 MB |
  | relation indexes | 68 MB |
  | list projections | 6 MB |

- The production master-data Redis holds 524 MB with no evictions yet. One
  data change could push it into the same failure mode.

Using Redis as both store and cache causes other defects:

- **Readiness depends on process-local state.** `HasRegionIndex` reads only
  the in-process search-index LRU (`storage/master_data_redis_cache.go`). That
  LRU is disabled on `control` and never warmed on `serve`, so
  `GET /events/:region/:id` returns 503 on `serve` (verified on the test
  cluster's `serve` pod).
- **`serve` writes, although it is documented as read-only.** `CurrentEvent`
  stores a `currentevents` pseudo-entity through `StoreRegion`
  (`usecase/master_data_sync_usecase.go`), and `ListAll` and `ListByPage`
  rebuild `:order` lists on read.
- **Data writes are not fenced.** Only the Postgres status write checks the
  sync lease. The phase checks described in
  [distributed-sync-coordination](distributed-sync-coordination.md) are not
  implemented for Redis data writes.

## Goals

- PostgreSQL is the only system of record for master data. Losing or
  flushing Redis never loses data or changes a response.
- Keep the latency budget: under 1 s per request and never over 3 s, cold or
  warm. A request reads only the blocks that hold the records it needs.
- Store master data compactly. Game data is mostly arrays of same-shaped
  objects, so per-row JSON repeats every field name on every row.
- The Go process holds no derived master data, and `serve` never writes.
- Response bodies stay byte-for-byte identical, so the SDK does not change.
- Sync data writes are atomic per entity and fenced by the sync lease in the
  same transaction.

## Non-goals

- Changing any HTTP contract.
- Querying record contents with SQL. Records are opaque compressed blocks;
  see [Inspecting records](#inspecting-records).
- Moving list filtering or sorting into SQL. List projections keep the Go
  semantics that were verified byte-for-byte in
  [#134](https://github.com/Sekai-World/sekai-master-api/pull/134).
- Horizontal scaling of `control`. The design is compatible with the
  existing lease.

## Storage format

All numbers below were measured on JP master data: 419 files, 1 062 637
records, 264.6 MB of compact JSON.

| Format | Size | Share | Reads one record without its entity |
|---|---|---|---|
| compact JSON per row (`jsonb` rows) | 264.6 MB | 100% | yes |
| zstd per row (Redis today) | 164.4 MB | 62% | yes |
| zstd per row with a per-entity dictionary | 95.7 MB | 36% | yes |
| positional arrays, field names stored once per entity | 113.1 MB | 43% | yes |
| positional arrays with a per-entity dictionary | 71.0 MB | 27% | yes |
| **source JSON rows in key-sorted blocks of 32, zstd** | **24.9 MB** | **9%** | **yes, one block** |
| same, blocks in source order | 24.5 MB | 9% | yes, one block |
| same, blocks by key hash | 47.3 MB | 18% | yes, one block |
| positional arrays in blocks of 32, zstd | 16.8 MB | 6% | yes, one block |
| whole entity as one zstd frame (lower bound) | 11.6 MB | 4% | no |

What the measurements show:

- Field names account for 57% of per-row JSON. Values also repeat heavily
  across neighboring rows.
- Per-row compression cannot exploit either of these, because each row has
  too little context. Blocks of neighboring rows can.
- Inside a block, zstd already removes the repeated field names. Positional
  encoding saves only another 2–3 points on top, at the cost of a custom
  format that must tell an absent field apart from `null`. The design keeps
  the source JSON rows as they are.
- Blocks must group similar rows. Sorting rows by key keeps source neighbors
  together, because source files are mostly sorted by id, and it gives each
  block a key range for lookups. Hash buckets double the size.
- Reading one block of 32 rows (decompress and decode) takes 70–90 µs
  locally. The dev pod decodes roughly 8–10× slower, so a block read there is
  on the order of 1 ms.

Footprint for all five regions and all 461 entity kinds:

- record blocks: about 125 MB
- order key lists: about 4 MB
- relation index postings: tens of MB
- projections: about 10 MB

That is **under 0.3 GB** in about 170k block rows, instead of 2.5–3 GB in
5.2 M `jsonb` rows. The dev cut-over measured 339 MB in 175,766 blocks. The
relation index postings came out larger than estimated (see
[Dev cut-over results](#dev-cut-over-results)).

## Architecture

| Layer | Role |
|---|---|
| PostgreSQL | System of record: record blocks, order lists, relation index postings, list projections, version payloads, sync status and leases. Sync writes each entity in one fenced transaction. |
| Go process | Stateless. `serve` only reads; `control` and `standalone` sync. |
| Redis | Optional read-through cache, off in the first release (see [Caching](#caching)). Any key may vanish at any time. |

The storage interfaces from the Redis work stay the same, so handlers do not
change:

- `GetByID`, `GetByIDs`, `GetByCompositeKeys`
- `ListAll`, `ListByPage`, `ListByIndex`
- `LoadProjection`, `HasEntityRecords`
- the version payload

A new `storage.PostgresMasterDataStore` implements them. The domain
definitions in `internal/domain/masterdata` also stay: relation indexes,
projections, and canonical key parts. Composite storage keys move there from
`storage`, and so does the block sort key.

## Schema

The new migrations are PostgreSQL-only (see
[Decision 3](#3-drop-sqlite)). All timestamps are `timestamptz`.

```sql
-- One row per stored entity kind per region.
CREATE TABLE master_entities (
    region             text        NOT NULL,
    entity             text        NOT NULL,
    revision           text        NOT NULL,  -- sha256 over keys and bodies in source order
    source_digest      text,                   -- layout-tagged sha256 of the source file
    source_commit      text,
    record_count       integer     NOT NULL,
    order_keys         bytea       NOT NULL,   -- zstd JSON array of record keys in source order
    index_version      text        NOT NULL DEFAULT '',
    projection_version text        NOT NULL DEFAULT '',
    updated_at         timestamptz NOT NULL,
    PRIMARY KEY (region, entity)
);

-- Records, key-sorted, in blocks of up to 32 rows (or 64 kB of JSON).
CREATE TABLE master_blocks (
    region    text    NOT NULL,
    entity    text    NOT NULL,
    first_key text    COLLATE "C" NOT NULL,  -- block sort key of the first record
    body      bytea   NOT NULL,              -- zstd JSON array of [record_key, [position, ...], record]
    PRIMARY KEY (region, entity, first_key)
);

-- Relation index postings (masterdata.EntityIndexes), built at sync.
CREATE TABLE master_record_index (
    region      text   NOT NULL,
    entity      text   NOT NULL,
    index_name  text   NOT NULL,
    index_key   text   NOT NULL,   -- masterdata.IndexKeys canonical form
    record_keys text[] NOT NULL,   -- in source order
    PRIMARY KEY (region, entity, index_name, index_key)
);

-- List projections (masterdata.ProjectionFields): gob + zstd, as today.
CREATE TABLE master_projections (
    region     text        NOT NULL,
    entity     text        NOT NULL,
    version    text        NOT NULL,
    body       bytea       NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (region, entity)
);

-- versions.json per region (today :versions-latest).
CREATE TABLE master_versions (
    region        text        PRIMARY KEY,
    payload       jsonb       NOT NULL,
    source_commit text,
    updated_at    timestamptz NOT NULL
);
```

### Blocks

- **Record key.** Today's storage ID: the decimal `id`, a composite key
  (`resourceboxes`, `resourceboxdetails`,
  `charactermissionv2parametergroups`), or an `auto:` hash.
- **Sort key.** The order records are blocked in and located by:
  - a decimal key of up to 20 digits becomes `"0"` + the key zero-padded to
    20 digits, so numbers sort numerically;
  - every other key becomes `"1"` + the key.

  The column uses `COLLATE "C"`, so Postgres compares bytes exactly as Go
  does. A unit test pins the encoding.
- **Block boundaries.** A block closes at 32 record keys, or on the record
  that brings it to 64 kB of JSON, whichever comes first. Large records such
  as gachas (about 27 kB each) then share only small blocks.
- **Block body.** A zstd frame of a JSON array of `[record_key, [position,
  ...], record]`. `record` is the compact source JSON, byte for byte, so even
  key order and number formatting survive. A key the source repeats (records
  with the same id, or identical records without one) is one entry with
  every position, and each position reads the record stored last under the
  key, exactly as the Redis store answers.
- **Why sort by key.** Source files are mostly sorted by `id`, so key order
  keeps source neighbors together. It compresses like source order: 24.9 MB
  versus 24.5 MB for JP. It also gives every block a key range, so a key is
  found with one index probe instead of a 5.2 M-row key directory.
- **Source order.** Kept separately in `master_entities.order_keys`: 0.8 MB
  compressed for all of JP.

### Relation index postings

- There is one row per index value: for example `resourceboxes` by `id`,
  about 21k rows in JP. Each row holds the record keys in source order.
- Using a table, rather than SQL expression or GIN indexes, keeps
  `masterdata.EntityIndexes` as the single definition, including nested-array
  paths (`gachaPickups.cardId`), multi-field keys and canonical number
  formatting.

## Read path

**Consistency rule.** Every storage read that must see one version of an
entity is one SQL statement, so it sees a single snapshot. Readers never mix
the index or order of one sync with the blocks of another.

| Method | Statement |
|---|---|
| `GetByID`, `GetByIDs`, `GetByCompositeKeys` | For each requested sort key, a `LATERAL` probe returns the block with the greatest `first_key <= key`. Blocks are deduplicated. Go decodes them in parallel, picks the requested records and aligns them to the input. |
| `ListAll` | All blocks of the entity. Go decodes them in parallel and orders the records by `position`. |
| `ListByPage` | First `record_count` and `order_keys`; Go slices the page's keys. Then the `GetByIDs` statement. Across a concurrent sync, a key removed between the two statements is skipped, which matches today's Redis behavior. |
| `ListByIndex` | The entity's index version, the postings for the lookup values, and a block probe for every posted key, in one statement. The probe computes sort keys with the SQL function `master_block_sort_key()`, which a test pins to the Go function. Go returns records in posting order. |
| `LoadProjection` | `SELECT version, body FROM master_projections WHERE region = $1 AND entity = $2` |
| `HasEntityRecords` | `SELECT record_count > 0 FROM master_entities WHERE ...` |
| version payload | `SELECT payload FROM master_versions WHERE region = $1` |

Behavior that stays the same:

- An entity with no `master_entities` row returns empty results.
- A stale `index_version` or `projection_version` is answered from the
  entity's blocks, with the existing warning.

Behavior that changes:

- **Reads never write.** The `:order` rebuild on read and the `currentevents`
  store are removed; see [Current event](#current-event).
- **One connection pool.** Master data uses a native `pgxpool.Pool`, because
  it needs arrays, `COPY` and `LATERAL` probes with typed parameters. The
  existing `database/sql` repositories for status and lease share the same
  pool through `stdlib.OpenDBFromPool`. Pool size and timeouts become
  configuration.

Expected latency:

- An in-cluster statement takes 1–3 ms.
- A page of 20 records touches at most 20 blocks, so about 20 ms of decoding
  on the dev pod.
- A relation lookup covering a few hundred records touches a few dozen
  blocks.

[Verification](#verification) measures this before cut-over.

## Write path (sync)

Per entity file, on `control` and `standalone`:

1. **Skip.** When `source_digest`, `index_version` and `projection_version` in
   `master_entities` all match, the file is skipped without parsing. The
   digest skip is safe for composite-key entities because their keys are
   deterministic.
2. **Collect.** Build the record keys, compact bodies and positions, and the
   revision: sha256 over keys and bodies in source order. The revision no
   longer hashes zstd-compressed bytes. Relation index postings and the
   projection are built exactly as today.
3. **Write, in one transaction per entity** (read committed):
   1. **Fence.**
      `SELECT fencing_token FROM master_data_sync_leases WHERE name = $1 FOR SHARE`.
      Abort with `ErrFencedOut` unless the token matches the one this sync
      holds. This closes the data-write gap in
      [distributed-sync-coordination](distributed-sync-coordination.md#fencing-model).
      Sync jobs carry their token in the context
      (`masterdata.WithFencingToken`). Writes without a token pass, as they
      do for sync status, when the lease is disabled.
   2. **Records.** If the revision differs, `DELETE` the entity's blocks,
      `COPY` the new key-sorted blocks, and write `order_keys`. Whole-entity
      rewrites are cheap: all of JP's blocks total about 25 MB, and the
      largest entity, `costume3ds`, is about 3 MB. They also avoid row-level
      diffing.
   3. **Postings.** If the records changed or `index_version` differs,
      delete the entity's postings and `COPY` the new ones.
   4. **Projection.** If the records changed or `projection_version` differs,
      upsert the projection.
   5. **Entity row.** Upsert `master_entities` with the revision, digest,
      count and versions.
4. **Parallelism.** Up to `MASTER_DATA_REGION_FILE_CONCURRENCY` entity
   transactions run at once. The pool must be larger than region concurrency
   × file concurrency.
5. **Removed entities.** After a successful full region load, entity kinds
   the source no longer has are deleted in one fenced transaction. Today such
   entities stay in Redis forever.

Expected cost:

- A first full load writes about 170k block rows (about 125 MB) for five
  regions. It is bounded by JSON parsing and zstd in Go, not by the database.
- A routine sync rewrites only the entities that changed.

Consistency across entities matches today: while a region syncs, readers can
see new versions of some entities next to old versions of others.

Unchanged:

- The commit-unchanged and `versions.json`-unchanged skip paths. Their "store
  has data" check becomes `SELECT count(*) FROM master_entities WHERE region =
  $1`, instead of the Redis rebuild flag.
- `EnsureDerivedEntityData`, which rebuilds stale postings and projections
  from the entity's blocks.

## Current event

- `CurrentEvent` no longer stores a `currentevents` pseudo-entity.
- It reads the `events` list projection, which includes the window fields
  that `evaluateCurrentEventRange` compares (`id`, `startAt`, `closedAt`).
- It picks the current event in memory, over about 216 JP rows, then reads
  that one record with `GetByID`.

`serve` becomes truly read-only.

A store synced by an older build has no stored `events` projection yet. Until
the next sync stores it, `LoadProjection` builds it from every event record on
each call, which is correct but slow. So `control` must run a sync before
`serve` takes traffic on the new build, as the rollout order in step 6 already
requires.

## Readiness

- **Data readiness for a region** is the latest persisted sync status
  `success` plus a `master_entities` row with `record_count > 0` for the
  entity. That is one or two indexed queries with no process state. An
  entity the region's source does not have (CN has no `gameNews`) is ready
  once the region holds any records, so its lists read as empty instead of
  `503`; a region with no records at all stays not ready.
- **Removed with the search index:**
  - `HasRegionIndex`, `RuntimeSearchIndexReadyRegions`, the search-index LRU,
    the persisted search index, `Search` (no HTTP caller), startup warm-up and
    `EnsureConfiguredRegionIndexes`;
  - the settings `MASTER_DATA_WARM_SEARCH_INDEXES` and
    `MASTER_DATA_SEARCH_INDEX_CACHE_ENTRIES`;
  - the `sekai_master_data_region_index_*` metrics.

  This fixes the `serve` 503 on `GET /events/:region/:id` and the empty
  `unitProfiles` availability response.
- **`serve /readyz`** checks the Postgres ping, plus, for every configured
  region, `cards` records and a complete version payload. Redis is not
  required.
- **Runbook:**
  - "Redis loss rebuild" becomes "cache flush: no action".
  - A new "Postgres restore" section is added.
  - The `SekaiRedisDataPlaneEmpty` and `SekaiSearchIndexNotReady` alerts are
    replaced by Postgres data-plane alerts: a region `record_count` of 0, and
    sync write errors.

## Inspecting records

Record blocks are opaque to SQL. Operators inspect data with the `dump`
subcommand (`cmd/api/dump.go`), which reads `DATABASE_URL` like the API:

```
sekai-master-api dump --region jp --entity eventcards [--key 1 | --index eventId=150]
```

- `--key 1` prints one record by ID.
- `--key resourceBoxId=5,resourceBoxPurpose=mission_reward,seq=1` prints one
  record of a composite-key entity, with its key fields.
- `--index eventId=150` prints the records a relation index matches. The
  index is named by its fields in order, for example
  `--index resourceBoxId=5,resourceBoxPurpose=mission_reward`.
- Without either flag, it prints every record of the entity in stored order.

It reads through the same store code and prints the decoded records as JSON,
with object keys sorted. It exits non-zero when a key has no record. The
source repositories on GitHub remain the canonical copy of every record.

## Local backups

The file backup store (`tmp/master-data-backup`, an emptyDir by default)
exists only because Redis could lose data.

- **Removed:** the dev bootstrap restore, the unchanged-commit backup restore
  and the manifest-skip restore from backup payloads.
- **Recovery:** the WAL-G backups of the Postgres instance, or a forced sync
  from GitHub.
- **Manifest skip:** it keeps working, comparing against `master_versions`.
- **`VersionByRegion`:** it reads only `master_versions`.

## Caching

The first release has no Redis read path. Postgres reads are expected to fit
the budget, and a cache adds invalidation and consistency work that
measurements must justify. If a measured hotspot needs one, the rule is:

- Read-through only. Keys embed the entity revision, for example
  `<region>:<entity>:<revision>:projection`, so a new sync can never serve
  stale data and nothing needs invalidating.
- Every key has a TTL, and LRU eviction is fine.
- Redis is never a write target and never a readiness input.

`REDIS_ADDR` becomes optional. The master-data Redis instances can be
downsized or removed once nothing reads them.

## Testing

- **Postgres tests.** Storage and repository tests run against PostgreSQL 18
  through `testcontainers-go` on the host Docker API, which `AGENTS.md` already
  allows for test dependencies. CI `ubuntu-latest` provides Docker. Locally,
  the tests skip with a clear message when Docker is unavailable, unless `CI`
  is set.
- **Migrations and repositories.** These tests run the migrations and cover
  the status and lease repositories, which no test covers today.
- **Contract suite.** One `storage` contract suite covers:
  - ordering, composite keys and sort-key boundaries (mixed numeric and
    composite keys, block splits by row count and by bytes);
  - index and projection rebuilds;
  - removal of entities the source dropped;
  - fencing;
  - empty regions.

  During migration it runs against both the Redis and the Postgres store,
  which proves parity before cut-over.
- **Handler tests** keep their in-memory fakes, because the handler contract
  is unchanged.

## Infrastructure (`sekai-k3s-infra`)

- **Location.** The master data goes into `sekai_master_api` on
  `postgresql-sekai-viewer`. That is the shared instance and database the API
  already uses for sync status and leases:
  - Postgres 18.6;
  - a 32 Gi volume with 126 MB used;
  - WAL-G backups to S3.

  No new instance or database is needed, and the added footprint is under
  0.3 GB.
- **New role.** A read-only role for `serve`, with its own `DATABASE_URL`
  secret.
- **Redis.** The eviction policy of the master-data Redis instances stays
  unchanged ([Decision 7](#7-redis-eviction-policy)).

## Rollout

Each step is its own PR, with tests, lint and a dev-cluster check.

1. **Infra.** Add the read-only `serve` role and secret. Done in
   Sekai-World/sekai-k3s-infra#354:
   - The `postgresql-sekai-viewer` reconcile job creates
     `sekai_master_api_reader` with `CONNECT` on `sekai_master_api`. It gets
     `SELECT` on current tables, and on future ones through default
     privileges.
   - `sekai-master-api-serve-secrets` holds its `DATABASE_URL`. No workload
     uses it yet.
2. **Groundwork.** Done:
   - Drop SQLite. `DATABASE_DRIVER` stays optional but accepts only `pgx`;
     any other value fails startup.
   - Add one `pgxpool`, with `database/sql` bridged to it
     (`storage.OpenDB` returns both). Pool size and timeouts are the
     `DATABASE_*_CONNS` and `DATABASE_*_SECONDS` settings.
   - Add the testcontainers harness (`internal/storage/pgtest`) and run it in
     CI. It uses the Debian `postgres:18` image, whose default collation is
     not byte order, so a missing `COLLATE "C"` fails the tests.
   - Add the PostgreSQL-only migrations for the new tables.
   - Move composite keys and the block sort key into `masterdata`
     (`record_key.go`).
3. **New store.** Add `PostgresMasterDataStore` (read and write, block codec)
   behind `MASTER_DATA_STORE=redis|postgres`, defaulting to `redis`. Run the
   contract suite against both stores. Done:
   - The contract suite (`master_data_store_contract_test.go`) runs every
     scenario against both stores, and a parity test compares about 1,000
     reads between them.
   - `TestStoreParityOnSourceDirectory` compares both stores on a real
     checkout. On TW (455 files) all 4,121 reads matched; the store wrote the
     region in 5.4 s and used 87.8 MB on disk in 37,247 blocks.
   - Sync passes the lease token to data writes, prunes dropped entities after
     a full load, and uses `HasRegionData` where the Redis store rebuilt its
     search index.
   - `CurrentEvent` wrote a `currentevents` cache until step 5, so `serve`
     on the PostgreSQL store needed write access.
4. **Dev cut-over.** Done on 2026-09-28; dev now runs on `postgres` (see
   [Dev cut-over results](#dev-cut-over-results)).
   - Set `postgres` on dev and run a full sync.
   - Compare every public route byte for byte against a Redis-backed build of
     the same commit.
   - Time every route cold and warm, and time the sync.
5. **Make Postgres the default.**
   - Remove the Redis store, the search index, the LRU, the local backups and
     Redis readiness. Done: `MASTER_DATA_STORE` stays optional but accepts
     only `postgres`, and the `REDIS_*` and search-index settings are gone.
     The manifest skip compares against the stored version payload, and the
     rate-limit fallback keeps the stored data. `serve /readyz` reports a
     store read error as `database`.
     The `sekai_master_data_region_index_*` and `sekai_redis_*` metrics are
     replaced by `sekai_master_data_region_records{region}`.
     The alerts are now `SekaiMasterDataRegionEmpty` (a region with no
     records) and `SekaiMasterDataSyncFailed`, which now matches the `failed`
     status that sync writes.
   - Make `CurrentEvent` read-only through the events projection. Done: the
     `events` projection keeps `id`, `startAt` and `closedAt`, and
     `CurrentEvent` reads only the chosen record. A `currentevents` entity
     left by older builds is ignored; a full sync prunes it from Postgres.
   - Add the `dump` subcommand. Done; see [Inspecting records](#inspecting-records).
   - Update the docs, the runbook and `AGENTS.md`. Done.
   - The Helm chart is unchanged in this step. The test cluster's Argo CD
     application renders the chart from `main` with a pinned image, so chart
     changes would reach the running release before its image does. The
     chart's backup volume, Redis egress rule, Redis readiness notes and
     README move to step 6.
6. **Production cut-over.** Deploy `control` first, run a full sync and
   verify, then deploy `serve`. Redis becomes optional. Update the chart in
   the same change: drop the backup volume and the Redis settings and
   egress, and switch `serve` to the read-only `sekai-master-api-serve-secrets`.
   Progress on 2026-09-28:
   - The release pipeline publishes again (#143). `docker.dnaroma.eu` was
     gone, so images now go through Zot's public write-only push endpoint.
     `v0.2.0` was the first release through it.
   - The test cluster runs `0.2.0` (sekai-k3s-infra#356). The migration Job
     applied the new tables. `control`'s startup sync found no region data
     in PostgreSQL and fully synced `jp` and `en` in 87 s, while the `0.0.4`
     `serve` kept serving until the new pod's `/readyz` passed.
   - Its 790 `jp`/`en` routes return the same bodies as dev, except the
     `availableRegions` of event details: test configures two regions.
   - The first measurements were slow, because the scheduler had placed the
     pods on a node in another data center than PostgreSQL. A required pod
     affinity now keeps them next to the database (sekai-k3s-infra#359).
     `control` then stayed `Pending`, because its `local-path` backup PVC was
     bound to the old node. The backup volume was dropped
     (sekai-k3s-infra#360). The PVC itself is annotated `Prune=false` and is
     deleted by hand.
   - `serve` connects as the read-only `sekai_master_api_reader` through
     `sekai-master-api-serve-secrets`, and `control` and the migration Job
     keep the owner credential (sekai-k3s-infra#361). `pg_stat_activity`
     shows `serve` connected as the reader.
   - The chart drops the backup volume, the Redis egress port and the Redis
     documentation.
   - Step 6 is done. The master-data Redis instances no longer have a
     reader. Removing them is a separate infra change.
7. **Cache, only if measurements require it:** a revision-keyed Redis
   read-through cache.

The remaining list projections (events, musics and virtual lives, with their
cross-entity filters) continue after step 5, on Postgres.

## Verification

A step counts as done only when all of these hold:

- Every public route returns identical bytes on the Redis-backed and the
  Postgres-backed builds of the same commit.
- Every route stays under 1 s cold and warm on dev, and no route reaches 3 s.
- A full five-region sync completes and is timed, and a no-op re-sync skips
  every entity.
- The Postgres footprint is measured against the estimate of under 0.3 GB.
- Flushing Redis, or running without it, changes no response.
- A sync holding a stale fencing token cannot write data.
- `serve` issues no writes, which the read-only role enforces.

## Dev cut-over results

Rollout step 4, measured on the dev cluster on 2026-09-28 with commit
`c0fc0fa`.

**Method.**

1. The dev Redis was flushed.
2. A Redis-backed build synced `jp` and `en` (about 540 MB, below the 1.5 GB
   cap, with no new evictions).
3. We captured 855 URLs, each fetched twice (cold, then warm):
   - every public GET route in the swagger, for both regions;
   - `{id}` routes filled with real ids sampled from the list endpoints;
   - page, spoiler and sort variants of every list;
   - the costume, card, gacha, event, music, mission and Kizuna filter
     queries.
4. The same commit, switched to `MASTER_DATA_STORE=postgres`, synced the same
   regions, and the same URLs were captured again.
5. The Postgres build then synced all five regions, and every route was timed.

**Correctness.**

- 854 of the 855 responses were byte-identical, status included.
- The one exception is `/build-info`, whose `buildDate` differs because the
  two images were built at different times.
- The Postgres process never connected to Redis. JP `actionSets`, which had
  returned 503 on Redis after evictions, now returns 200.

**Sync.**

| Region | Redis | Postgres |
|---|---|---|
| jp (419 files) | 58.8 s | 35.3 s |
| en (404 files) | 51.0 s | 31.2 s |
| tw (455 files) | — | 35.0 s |
| kr (455 files) | — | 37.1 s |
| cn (437 files) | — | 35.1 s |

A no-op re-sync of all five regions skipped every region in 1.5 s.

**Footprint.** 339 MB for 5,541,830 records in 2,110 entities:

| Table | Size |
|---|---|
| `master_blocks` (175,766 blocks) | 193 MB |
| `master_record_index` (293,214 postings) | 109 MB |
| `master_entities` (including order keys) | 31 MB |
| `master_projections` | 5.7 MB |

The Redis store held about 1.5 GB for the same five regions.

**Latency (dev, through the port-forward).**

On the same 855 `jp` and `en` URLs:

| Store | Median | p95 | Max |
|---|---|---|---|
| Redis | 115 ms | 242 ms | 908 ms |
| Postgres | 123 ms | 343 ms | 1048 ms |

All five regions, 2,026 URLs: median 118 ms, p95 348 ms, p99 627 ms, maximum
999 ms. No route reached 1 s in that run.

Routes that are slower than on Redis:

| Route | Postgres | Redis |
|---|---|---|
| card list, 100 per page | 0.87–1.05 s | 0.38 s |
| music list, 100 per page | about 0.8 s | 0.41 s |
| event rewards | 0.6–0.74 s | 0.30 s |

Each of these reads related records one at a time:

- the card list reads a card's supply, skill and character per card;
- the music list reads a music's artist and stage per music;
- event rewards read each reward's box, item and title per detail.

A single read costs about 1 ms on Redis and about 3 ms on Postgres. These
routes now read related records with one `GetByIDs` per entity
(`shared.PrefetchRecords`). On `dev-0ee3225`, the median of five warm requests:

| Route | Before | After |
|---|---|---|
| card list, 100 per page | 782 ms | 164 ms |
| music list, 100 per page | 814 ms | 186 ms |
| event rewards (events 1, 100, 201, 211) | 340–537 ms | 125–130 ms |

All 2,026 URLs returned the same bytes as before the change.

## Decisions

Decided on 2026-09-28.

### 1. Instance

The data goes on the shared instance. The API's existing `sekai_master_api`
database on `postgresql-sekai-viewer` holds the new tables.

### 2. Which entities to store

All 461 entity kinds. Postgres stays a full mirror of the source, so new
endpoints need no sync change. The compact block format makes the cost
negligible.

### 3. Drop SQLite

Yes. This includes `values-development.yaml` and the SQLite branches in the
repositories.

### 4. Tests need Docker

Accepted. The testcontainers harness runs in CI and skips locally without
Docker.

### 5. Read-only role for `serve`

Accepted.

### 6. Delete entity kinds removed from the source

Accepted.

### 7. Redis eviction policy

The master-data Redis instances keep their eviction policy
(`allkeys-lru`). An interim `noeviction` was proposed and declined: the fix
is moving master data to PostgreSQL, not reconfiguring Redis.
