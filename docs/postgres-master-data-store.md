# PostgreSQL Master-Data Store — Design

Status: proposed, awaiting approval of the open decisions below.

This document specifies how `sekai-master-api` moves master data from Redis,
which today is its only store, to PostgreSQL. Redis becomes an optional,
disposable cache. It is the design gate before implementation.

## Problem

Redis is the system of record for master data, but Redis is configured and
operated as a cache:

- Both master-data Redis instances run with
  `--maxmemory 1536mb --maxmemory-policy allkeys-lru`
  (`sekai-k3s-infra/clusters/test/platform/redis-sekai-master*/redis.yaml`).
- On 2026-09-28 the dev instance reached 1.45 GB and had **evicted 3436 keys**
  (`INFO stats`). One of them was JP `actionsets:by-id`, so
  `/api/v1/actionSets/jp/list` returned 503 `REGION_DATA_NOT_READY`. Evictions
  are silent, and nothing rebuilds the lost keys until the next full sync.
- Dev memory by key kind (5 regions, `MEMORY USAGE` over all 9974 keys):

  | Keys | Memory |
  |---|---|
  | records (`:by-id`) | 1280 MB |
  | search indexes (no HTTP consumer) | 129 MB |
  | order lists | 97 MB |
  | relation indexes | 68 MB |
  | list projections | 6 MB |

- The production master-data Redis holds 524 MB with no evictions yet. It is
  one data change away from the same failure mode.

Other defects follow from Redis being both the store and a cache:

- Readiness depends on process-local state. `HasRegionIndex` reads only the
  in-process search-index LRU (`storage/master_data_redis_cache.go`). That LRU
  is disabled on `control` and never warmed on `serve`, so
  `GET /events/:region/:id` returns 503 on `serve` (verified on the test
  cluster's `serve` pod).
- `serve` is documented as read-only but writes. `CurrentEvent` stores a
  `currentevents` pseudo-entity through `StoreRegion`
  (`usecase/master_data_sync_usecase.go`), and `ListAll`/`ListByPage` rebuild
  `:order` lists on read.
- Only the Postgres status write is fenced. Data writes to Redis are not
  fenced (`docs/distributed-sync-coordination.md` describes phase checks that
  are not implemented).

## Goals

- PostgreSQL is the only system of record for master data. Losing or
  flushing Redis never loses data or changes responses.
- Keep the latency budget: under 1 s per request, never over 3 s, cold or
  warm. Every request reads only the rows it needs.
- The Go process holds no derived master data. `serve` never writes.
- Response bodies stay byte-for-byte identical, so the SDK does not change.
- Sync data writes are atomic per entity and fenced by the sync lease in the
  same transaction.

## Non-goals

- Changing any HTTP contract.
- Moving list filtering or sorting into SQL. List projections keep the
  current Go semantics, which were verified byte-for-byte in
  [#134](https://github.com/Sekai-World/sekai-master-api/pull/134).
- Horizontal scaling of `control`. This design is compatible with the
  existing lease.

## Data volume

| Measure | JP | Five regions |
|---|---|---|
| Source files | 419 | about 2 050 |
| Records | 1 062 637 | about 5.16 M (Redis baseline: 5 156 574) |
| Compact JSON | 264 MB | about 1.3 GB |

Only about 67 of the 461 stored entity kinds are read by handlers. The other
kinds hold about 65% of the bytes: the `costume3dmodel*` and
`costume3dshop*` tables alone are about 630 MB across regions.

Estimated PostgreSQL footprint if every entity is stored:

- 1.3–1.6 GB of `jsonb` (rows over 2 kB are TOAST-compressed with lz4)
- about 0.2 GB of row headers
- about 0.6 GB for the primary-key and position indexes
- about 0.1 GB for relation index rows and projections

That totals **2.5–3 GB**, plus room for WAL and update bloat.

## Architecture

| Layer | Role |
|---|---|
| PostgreSQL | System of record. Holds records, relation indexes, list projections, version payloads, sync status and leases. Sync writes each entity in one fenced transaction. |
| Go process | Stateless. `serve` only reads; `control`/`standalone` sync. |
| Redis | Optional read-through cache, off in the first release (see [Caching](#caching)). Any key may vanish at any time. |

The storage interfaces from the Redis work stay the same: `GetByID`,
`GetByIDs`, `GetByCompositeKeys`, `ListAll`, `ListByPage`, `ListByIndex`,
`LoadProjection`, `HasEntityRecords`, and the version payload. A new
`storage.PostgresMasterDataStore` implements them, so handlers do not change.
The domain definitions in `internal/domain/masterdata` (relation indexes,
projections, canonical key parts) also stay; composite storage keys move
there from `storage` so both stores share them.

## Schema

The new migrations are PostgreSQL-only (see
[Decision 3](#3-drop-sqlite)). All timestamps are `timestamptz`.

```sql
-- One row per stored entity kind per region.
CREATE TABLE master_entities (
    region             text        NOT NULL,
    entity             text        NOT NULL,
    revision           text        NOT NULL,  -- sha256 over keys, bodies, and order
    source_digest      text,                   -- layout-tagged sha256 of the source file
    source_commit      text,
    record_count       integer     NOT NULL,
    index_version      text        NOT NULL DEFAULT '',
    projection_version text        NOT NULL DEFAULT '',
    updated_at         timestamptz NOT NULL,
    PRIMARY KEY (region, entity)
);

-- Every record. record_key is today's storage ID: the decimal id, a
-- composite key (resourceboxes, resourceboxdetails,
-- charactermissionv2parametergroups), or an auto: hash.
CREATE TABLE master_records (
    region     text    NOT NULL,
    entity     text    NOT NULL,
    record_key text    NOT NULL,
    position   integer NOT NULL,   -- source order; replaces the :order list
    body       jsonb   NOT NULL,
    PRIMARY KEY (region, entity, record_key)
);
ALTER TABLE master_records ALTER COLUMN body SET COMPRESSION lz4;
CREATE INDEX master_records_order ON master_records (region, entity, position);

-- Relation index entries (masterdata.EntityIndexes), built at sync.
CREATE TABLE master_record_index (
    region     text    NOT NULL,
    entity     text    NOT NULL,
    index_name text    NOT NULL,
    index_key  text    NOT NULL,   -- masterdata.IndexKeys canonical form
    position   integer NOT NULL,
    record_key text    NOT NULL,
    PRIMARY KEY (region, entity, index_name, index_key, position, record_key)
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

Why these shapes:

- **Bodies are `jsonb`, not compressed `bytea`.**
  - Records stay inspectable and queryable with SQL, which operators need once
    Postgres is the store.
  - `jsonb` normalizes key order and duplicate keys. Handlers decode into maps
    and `encoding/json` sorts map keys, so responses do not change. The
    parity check in [Verification](#verification) proves this.
- **Relation indexes are a table, not expression or GIN indexes.**
  - The definitions already live in Go, including nested-array paths
    (`gachaPickups.cardId`), multi-field keys, and canonical number
    formatting. Rebuilding them as SQL expressions would duplicate that logic
    and need DDL for every definition change.
  - A table keeps one source of truth, is written in the same transaction as
    the records, and answers
    `(region, entity, index_name, index_key) → ordered record_keys` with one
    index range scan.
- **Projections stay opaque `bytea`.** Decoding JP's 124k-row 3D costume
  projection takes about 8 ms. Their filter and sort semantics are proven
  identical in Go.
- **No partitioning in the first release.** Region-scoped primary keys and
  indexes are enough at about 5 M rows. List partitioning by region stays an
  option if vacuum or bloat on the whole table becomes a problem.

## Read path

Queries per interface method:

| Method | Query |
|---|---|
| `GetByID` / `GetByIDs` | `SELECT record_key, body FROM master_records WHERE region=$1 AND entity=$2 AND record_key = ANY($3)`, re-aligned to the input order in Go |
| `GetByCompositeKeys` | Same query, with keys encoded by the shared composite-key helper |
| `ListAll` | `... WHERE region=$1 AND entity=$2 ORDER BY position` |
| `ListByPage` | The same with `LIMIT/OFFSET`; the total comes from `master_entities.record_count` |
| `ListByIndex` | `SELECT i.index_key, r.body FROM master_record_index i JOIN master_records r USING (region, entity, record_key) WHERE i.region=$1 AND i.entity=$2 AND i.index_name=$3 AND i.index_key = ANY($4) ORDER BY i.index_key, i.position` |
| `LoadProjection` | `SELECT version, body FROM master_projections WHERE region=$1 AND entity=$2` |
| `HasEntityRecords` | `SELECT record_count > 0 FROM master_entities WHERE ...` |
| version payload | `SELECT payload FROM master_versions WHERE region=$1` |

Rules:

- Bodies are decoded in Go with the existing worker pool: chunks of 32
  records across all cores.
- `ListByIndex` and `LoadProjection` keep their current fallbacks.
  - An entity with no rows in `master_entities` returns empty results.
  - An entity whose `index_version` or `projection_version` is stale is
    answered from `master_records`, with the existing warning.
- A new native `pgxpool.Pool` serves master data, because arrays and `COPY`
  need it. The existing `database/sql` repositories (status, lease) share the
  same pool through `stdlib.OpenDBFromPool`, so there is one pool per process.
  Pool size and timeouts become configuration.
- Nothing on the read path writes. The `:order` rebuild on read and the
  `currentevents` store disappear (see [Current event](#current-event)).

Expected latency:

- An in-cluster query takes 1–3 ms.
- A page of 20 records, a relation lookup, or a projection (about 0.6 MB
  compressed for JP 3D costumes) adds a few milliseconds of transfer and
  decode.
- Every endpoint therefore stays in the same range as today's Redis reads.
  [Verification](#verification) measures this before cut-over.

## Write path (sync)

Per entity file, on `control`/`standalone`:

1. **Skip.** If `master_entities.source_digest`, `index_version` and
   `projection_version` all match, skip without parsing. The digest skip
   remains safe for composite-key entities, because their keys are
   deterministic.
2. **Collect.**
   - Canonical storage keys.
   - Compact bodies.
   - Position.
   - The revision: sha256 over key, body and order. It no longer hashes
     zstd-compressed bytes.
   - Relation index entries and the projection, built exactly as today.
3. **One transaction per entity** (`pgx.BeginTx`, read committed):
   1. **Fence.** `SELECT fencing_token FROM master_data_sync_leases WHERE name = $1 FOR SHARE`.
      Abort with `ErrFencedOut` if the token is not the one this sync holds.
      Data writes are then fenced by the lease, closing the gap in
      [distributed-sync-coordination](distributed-sync-coordination.md#fencing-model).
   2. **Upsert changed records.** If the revision differs, `COPY` the
      collected rows into a temporary table (`ON COMMIT DROP`). Then:
      - `DELETE` rows missing from it;
      - `INSERT ... ON CONFLICT (region, entity, record_key) DO UPDATE SET body = EXCLUDED.body, position = EXCLUDED.position WHERE (master_records.body, master_records.position) IS DISTINCT FROM (EXCLUDED.body, EXCLUDED.position)`.

      Unchanged rows are not rewritten, which keeps WAL and bloat
      proportional to the change.
   3. **Relation indexes.** If records changed or `index_version` differs,
      delete the entity's `master_record_index` rows and `COPY` the new ones.
   4. **Projection.** If records changed or `projection_version` differs,
      upsert `master_projections`.
   5. **Entity row.** Upsert `master_entities` with the revision, digest,
      count and versions.
4. **Parallelism.** Up to `MASTER_DATA_REGION_FILE_CONCURRENCY` entity
   transactions run at once. The pool must be sized above region concurrency
   × file concurrency.
5. **Removed entities.** After a successful full region load, entity kinds
   that are no longer in the source are deleted in one fenced transaction.
   Today such entities stay in Redis forever.

Expected cost:

- The first full load of about 5.16 M rows is `COPY`-bound. At
  100–200 k rows/s that is about 30–60 s of database time for all regions.
- A routine sync rewrites only changed entities and changed rows.

Consistency matches today's per-entity atomicity: while a region syncs,
readers can see new versions of some entities next to old versions of
others. Region-level atomicity (a region generation switch) is possible
later but not required now.

Unchanged behavior:

- The commit-unchanged and `versions.json`-unchanged skip paths. Their "store
  has data" check becomes
  `SELECT count(*) FROM master_entities WHERE region = $1` instead of the
  Redis rebuild flag.
- `EnsureDerivedEntityData`. It rebuilds stale index and projection rows from
  `master_records`.

## Current event

- `CurrentEvent` stops storing a `currentevents` pseudo-entity.
- It reads the `events` list projection, which includes the window fields
  `evaluateCurrentEventRange` compares (`id`, `startAt`, `closedAt`). It picks
  the current event in memory, over about 216 JP rows, then reads that one
  record with `GetByID`.
- `serve` becomes truly read-only.

## Readiness

- **Data readiness for a region** is the latest persisted sync status
  `success` plus a `master_entities` row with `record_count > 0` for the
  entity. That is one or two indexed queries with no process state.
- **Removed:**
  - `HasRegionIndex`, `RuntimeSearchIndexReadyRegions`, the search-index LRU,
    the persisted search index, `Search`, startup warm-up and
    `EnsureConfiguredRegionIndexes`. `Search` has no HTTP caller.
  - Their configuration: `MASTER_DATA_WARM_SEARCH_INDEXES` and
    `MASTER_DATA_SEARCH_INDEX_CACHE_ENTRIES`.
  - Their metrics: `sekai_master_data_region_index_*`.

  This fixes the `serve` 503 on `GET /events/:region/:id` and the empty
  `unitProfiles` availability response.
- **`serve /readyz`** checks the Postgres ping, and for each configured region
  `cards` records plus a complete version payload. Redis is not required.
  Redis readiness is dropped, or reported as degraded-cache only when a cache
  is configured.
- **Runbook changes:**
  - "Redis loss rebuild" becomes "cache flush: no action".
  - A new "Postgres restore" section is added.
  - The alerts `SekaiRedisDataPlaneEmpty` and `SekaiSearchIndexNotReady` are
    replaced by Postgres data-plane alerts (region `record_count` of 0, sync
    write errors).

## Local backups

The file backup store (`tmp/master-data-backup`, an emptyDir by default) only
exists because Redis could lose data. With Postgres as the store:

- The restore paths go away: dev bootstrap restore, the unchanged-commit
  backup restore, and the manifest-skip restore that uses the backup payload.
- Recovery becomes a Postgres restore, or a forced sync from GitHub, which
  remains the source of truth.
- The manifest-skip path keeps working. It compares against
  `master_versions` instead of the backup `versions.json`.
- `VersionByRegion` reads `master_versions` only.

## Caching

The first release has no Redis read path, and this is deliberate:

- Postgres reads are expected to fit the budget.
- A cache adds invalidation and consistency work that must be justified by
  measurements.

If a measured hotspot needs a cache, the rule is:

- **Read-through only.** Keys embed the entity revision, for example
  `<region>:<entity>:<revision>:projection`, so a new sync can never serve
  stale data and no invalidation is needed.
- **Safe to lose.** Every key has a TTL, and LRU eviction is fine.
- **Never a write target, never a readiness input.**

`REDIS_ADDR` becomes optional. The master-data Redis instances can be
downsized or removed once nothing reads them.

## Testing

- **Real Postgres in tests.** Storage and repository tests run against
  PostgreSQL 18 through `testcontainers-go`, which uses the host's Docker
  API. This matches `AGENTS.md`: "Support running test dependencies through
  the host container engine using Docker API".
  - CI `ubuntu-latest` provides Docker.
  - Locally, the tests skip with a clear message when Docker is unavailable,
    unless `CI` is set.
- **Migrations and repositories.** Migrations run in those tests, which also
  cover the status and lease repositories. Today no test runs the migrations
  or the view queries.
- **Contract suite.** One `storage` contract suite covers store/read
  semantics: ordering, composite keys, index and projection rebuilds,
  removed-entity cleanup, fencing, and empty regions. During migration it
  runs against both the Redis and the Postgres store. That proves parity
  before cut-over.
- **Handler tests.** Handler tests keep their in-memory fakes; the handler
  contract is unchanged.

## Infrastructure (`sekai-k3s-infra`)

- **Database.** A `sekai_master` database and role on PostgreSQL 18, with a
  separate read-only role for `serve` if [Decision 5](#5-read-only-role-for-serve)
  is accepted.
- **Capacity for the store.** Today `postgresql-0` has an 8 Gi volume and
  requests 256 Mi of memory. The store needs:
  - a volume of at least 30 Gi;
  - a 1 Gi memory request;
  - `shared_buffers` of about 512 MB;
  - lz4 TOAST compression, which the schema sets per column
    (`SET COMPRESSION lz4`, so the server must be built with lz4, as the
    official images are);
  - autovacuum left on.
- **Backups.** WAL-G backups for whichever instance holds `sekai_master`,
  like `postgresql-sekai-viewer`, because it becomes a system of record.
- **Interim.** Until cut-over, set both master-data Redis instances to
  `--maxmemory-policy noeviction`. A full Redis then fails sync writes loudly
  instead of silently deleting data.

## Rollout

Each step is its own PR with tests, lint, and a dev-cluster check.

| Step | Change |
|---|---|
| 1 | Infra: database, role, volume, memory, backups; interim `noeviction`. |
| 2 | Groundwork: drop SQLite, add one `pgxpool` with `database/sql` bridged, run the testcontainers harness in CI, add PostgreSQL-only migrations for the new tables, move composite keys into `masterdata`. |
| 3 | `PostgresMasterDataStore`, read and write, behind `MASTER_DATA_STORE=redis\|postgres` (default `redis`). The contract suite runs against both stores. |
| 4 | Dev cut-over: set `postgres` on dev, full sync, then compare every public route byte-for-byte against a Redis-backed build of the same commit, and time all routes cold and warm plus sync duration. |
| 5 | Default `postgres`. Remove the Redis store, search index, LRU, local backups and Redis readiness. Make `CurrentEvent` read-only through the events projection. Update docs, runbook and `AGENTS.md`. |
| 6 | Production cut-over: deploy `control` first, full sync, verify, then `serve`. Redis becomes optional. |
| 7 | Only if measurements require it: a revision-keyed Redis read-through cache. |

The remaining list projections (events, musics, virtual lives and their
cross-entity filters) continue after step 5, on Postgres.

## Verification

A step counts as done only when all of these hold:

- Every public route returns identical bytes on the Redis-backed and the
  Postgres-backed build of the same commit.
- Every route stays under 1 s cold and warm on dev, and no route reaches
  3 s.
- A full five-region sync completes and is timed. A no-op re-sync skips every
  entity.
- Flushing Redis, or running without it, changes no response.
- A stale-token sync cannot write data (a fencing test).
- `serve` issues no writes. This is enforced by the read-only role if
  Decision 5 is accepted.

## Open decisions

### 1. Instance

The first option is a dedicated `postgresql-sekai-master` instance, with its
own resources, backups and blast radius. The second is a `sekai_master`
database on the shared `postgresql-0`: less to operate, but it competes with
the other applications' databases.
**Recommendation:** a dedicated instance, because it becomes a system of
record at 2.5–3 GB.

### 2. Which entities to store

The options are all 461 kinds (about 3 GB), or only kinds with a reader (a
code-maintained allowlist, about 1 GB).
**Recommendation:** store all of them in the first release. It keeps
Postgres a full mirror of the source, so new endpoints need no sync change,
and 3 GB fits the proposed volume.

### 3. Drop SQLite

The master-data store needs PostgreSQL-only features. Development already
runs on the remote cluster with `pgx`.
**Recommendation:** drop SQLite support, including `values-development.yaml`
and the SQLite branches in repositories.

### 4. Tests need Docker

The testcontainers harness needs Docker locally and in CI.
**Recommendation:** accept, and skip locally without Docker.

### 5. Read-only role for `serve`

A read-only role for `serve` enforces "serve never writes" in the database.
**Recommendation:** accept. It costs one more secret.

### 6. Delete entity kinds removed from the source

Deleting entity kinds that the source no longer has is a behavior change.
**Recommendation:** accept, since keeping them is a leak.
