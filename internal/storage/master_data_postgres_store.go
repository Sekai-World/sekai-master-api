package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/logging"
	"sekai-master-api/internal/tracing"
)

// PostgresMasterDataStore keeps master data in PostgreSQL
// (docs/postgres-master-data-store.md): records in compressed, key-sorted
// blocks, with order keys, relation index postings, list projections and
// version payloads next to them. Every write of one entity is one
// transaction, fenced by the sync lease when the write carries a lease token.
type PostgresMasterDataStore struct {
	pool            *pgxpool.Pool
	fileConcurrency int
	// leaseName names the master_data_sync_leases row whose token fences
	// writes; empty disables fencing.
	leaseName string
}

// postgresStoreLayoutVersion tags stored source digests. Bump it when the way
// records are keyed or encoded changes, so the next sync rewrites entities
// whose source did not change instead of skipping them.
const postgresStoreLayoutVersion = "1"

func postgresSourceDigest(digest string) string {
	return "pg-layout" + postgresStoreLayoutVersion + ":" + digest
}

// NewPostgresMasterDataStore returns a store on pool. fileConcurrency bounds
// the entity transactions one region store runs at once; leaseName, when not
// empty, fences writes by that sync lease.
func NewPostgresMasterDataStore(pool *pgxpool.Pool, fileConcurrency int, leaseName string) *PostgresMasterDataStore {
	if fileConcurrency <= 0 {
		fileConcurrency = 8
	}
	return &PostgresMasterDataStore{
		pool:            pool,
		fileConcurrency: fileConcurrency,
		leaseName:       strings.TrimSpace(leaseName),
	}
}

// Close is a no-op: the pool belongs to the caller.
func (store *PostgresMasterDataStore) Close() error {
	return nil
}

// --- Writes -----------------------------------------------------------------

func (store *PostgresMasterDataStore) StoreRegion(ctx context.Context, region string, payload map[string]any) error {
	return store.StoreRegionWithSourceDigests(ctx, region, payload, nil)
}

// StoreRegionWithSourceDigests stores every entity file of payload, each in
// its own transaction. Files that are not arrays of records are skipped. An
// entity whose source digest, index version and projection version all match
// the stored ones is skipped without parsing.
func (store *PostgresMasterDataStore) StoreRegionWithSourceDigests(ctx context.Context, region string, payload map[string]any, fileDigests map[string]string) error {
	ctx, span := tracing.StartSpan(ctx, "postgres.master_data.store_region", attribute.String("region", normalizeKey(region)), attribute.Int("file.count", len(payload)))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	regionName := normalizeKey(region)
	if regionName == "" {
		err = errors.New("region is required")
		return err
	}

	filePaths := make([]string, 0, len(payload))
	for filePath := range payload {
		filePaths = append(filePaths, filePath)
	}
	sort.Strings(filePaths)
	entityLocks := buildEntityLocks(filePaths)

	progressReporter := masterdata.ProgressReporterFromContext(ctx)
	var progressMu sync.Mutex
	processedFiles := 0
	reportProgress := func(filePath string) {
		progressMu.Lock()
		defer progressMu.Unlock()
		processedFiles++
		reportCacheWriteProgress(progressReporter, region, filePath, processedFiles, len(filePaths))
	}

	var errMu sync.Mutex
	var firstErr error
	semaphore := make(chan struct{}, effectiveFileConcurrency(store.fileConcurrency, len(filePaths)))
	var wait sync.WaitGroup
	for _, filePath := range filePaths {
		semaphore <- struct{}{}
		wait.Go(func() {
			defer func() { <-semaphore }()
			if storeErr := store.storeEntityFile(ctx, regionName, filePath, payload[filePath], fileDigests[filePath], entityLocks); storeErr != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = storeErr
				}
				errMu.Unlock()
			}
			reportProgress(filePath)
		})
	}
	wait.Wait()

	err = firstErr
	return err
}

func (store *PostgresMasterDataStore) storeEntityFile(ctx context.Context, region string, filePath string, value any, digest string, entityLocks map[string]*sync.Mutex) error {
	entity := entityNameFromPath(filePath)
	if entity == "" {
		return nil
	}
	rawRecords, isRaw := value.([]json.RawMessage)
	legacyRecords, isLegacy := value.([]any)
	if !isRaw && !isLegacy {
		return nil
	}

	lock := entityLocks[entity]
	lock.Lock()
	defer lock.Unlock()

	force := masterdata.ForceFullStoreFromContext(ctx)
	if !force && digest != "" {
		unchanged, err := store.sourceUnchanged(ctx, region, entity, digest)
		if err != nil || unchanged {
			return err
		}
	}

	collected, err := collectEntity(entity, rawRecords, legacyRecords)
	if err != nil {
		return fmt.Errorf("collect records region %s entity %s: %w", region, entity, err)
	}
	return store.writeEntity(ctx, region, entity, collected, digest, force)
}

// sourceUnchanged reports whether the stored entity came from the same
// source file and has current derived data.
func (store *PostgresMasterDataStore) sourceUnchanged(ctx context.Context, region, entity, digest string) (bool, error) {
	var storedDigest *string
	var indexVersion, projectionVersion string
	err := store.pool.QueryRow(ctx, `
SELECT source_digest, index_version, projection_version FROM master_entities
WHERE region = $1 AND entity = $2`, region, entity).Scan(&storedDigest, &indexVersion, &projectionVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read source digest region %s entity %s: %w", region, entity, err)
	}
	return storedDigest != nil && *storedDigest == postgresSourceDigest(digest) &&
		indexVersion == masterdata.IndexVersion(entity) &&
		projectionVersion == masterdata.ProjectionVersion(entity), nil
}

// collectedEntity is one entity's records, keyed, in source order, with the
// relation indexes and projection built from them.
type collectedEntity struct {
	keys       []string
	records    [][]byte
	revision   string
	indexes    map[string]map[string][]string
	projection *masterdata.ProjectionBuilder
}

// collectEntity keys the records and builds their derived data: every
// occurrence of a key feeds the indexes and the projection with its own
// record.
func collectEntity(entity string, rawRecords []json.RawMessage, legacyRecords []any) (*collectedEntity, error) {
	keyer := newRecordKeyer(entity)
	collected := &collectedEntity{
		keys:       make([]string, 0, len(rawRecords)+len(legacyRecords)),
		records:    make([][]byte, 0, len(rawRecords)+len(legacyRecords)),
		indexes:    newEntityIndexes(entity),
		projection: masterdata.NewProjectionBuilder(entity),
	}
	digest := sha256.New()
	add := func(key string, body []byte, record map[string]any) {
		collected.keys = append(collected.keys, key)
		collected.records = append(collected.records, body)
		if len(collected.indexes) > 0 || collected.projection != nil {
			if record == nil {
				record = rawRecordMap(body)
			}
			addToEntityIndexes(collected.indexes, key, record)
			if collected.projection != nil && record != nil {
				collected.projection.Add(key, record)
			}
		}
		_, _ = digest.Write([]byte(strconv.Itoa(len(key)) + ":" + key + ":" + strconv.Itoa(len(body)) + ":"))
		_, _ = digest.Write(body)
		_, _ = digest.Write([]byte{';'})
	}

	for _, value := range legacyRecords {
		record, ok := value.(map[string]any)
		if !ok {
			continue
		}
		body, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		if key := keyer.key(record, body); key != "" {
			add(key, body, record)
		}
	}
	for _, raw := range rawRecords {
		if len(raw) == 0 {
			continue
		}
		if key := keyer.keyFromRaw(raw); key != "" {
			add(key, raw, nil)
		}
	}
	collected.revision = hex.EncodeToString(digest.Sum(nil))
	return collected, nil
}

func (store *PostgresMasterDataStore) writeEntity(ctx context.Context, region, entity string, collected *collectedEntity, digest string, force bool) error {
	blocks, err := buildBlocks(blockEntries(collected.keys, collected.records))
	if err != nil {
		return fmt.Errorf("build blocks region %s entity %s: %w", region, entity, err)
	}
	orderKeys, err := encodeOrderKeys(collected.keys)
	if err != nil {
		return err
	}
	projection, err := encodeProjection(collected.projection)
	if err != nil {
		return fmt.Errorf("projection region %s entity %s: %w", region, entity, err)
	}

	return store.inFencedTx(ctx, func(tx pgx.Tx) error {
		var storedRevision, indexVersion, projectionVersion string
		err := tx.QueryRow(ctx, `
SELECT revision, index_version, projection_version FROM master_entities
WHERE region = $1 AND entity = $2 FOR UPDATE`, region, entity).Scan(&storedRevision, &indexVersion, &projectionVersion)
		found := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read entity region %s entity %s: %w", region, entity, err)
		}

		recordsChanged := force || !found || storedRevision != collected.revision
		if recordsChanged {
			if err := replaceBlocks(ctx, tx, region, entity, blocks); err != nil {
				return err
			}
		}
		if recordsChanged || indexVersion != masterdata.IndexVersion(entity) {
			if err := replacePostings(ctx, tx, region, entity, collected.indexes); err != nil {
				return err
			}
		}
		if recordsChanged || projectionVersion != masterdata.ProjectionVersion(entity) {
			if err := replaceProjection(ctx, tx, region, entity, projection); err != nil {
				return err
			}
		}

		var sourceDigest *string
		if digest != "" {
			tagged := postgresSourceDigest(digest)
			sourceDigest = &tagged
		}
		if recordsChanged {
			_, err = tx.Exec(ctx, `
INSERT INTO master_entities (
	region, entity, revision, source_digest, record_count, order_keys, index_version, projection_version, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (region, entity) DO UPDATE SET
	revision = EXCLUDED.revision,
	source_digest = EXCLUDED.source_digest,
	record_count = EXCLUDED.record_count,
	order_keys = EXCLUDED.order_keys,
	index_version = EXCLUDED.index_version,
	projection_version = EXCLUDED.projection_version,
	updated_at = EXCLUDED.updated_at`,
				region, entity, collected.revision, sourceDigest, len(collected.keys), orderKeys,
				masterdata.IndexVersion(entity), masterdata.ProjectionVersion(entity), time.Now().UTC())
		} else {
			_, err = tx.Exec(ctx, `
UPDATE master_entities SET
	source_digest = COALESCE($3, source_digest),
	index_version = $4,
	projection_version = $5,
	updated_at = $6
WHERE region = $1 AND entity = $2`,
				region, entity, sourceDigest, masterdata.IndexVersion(entity), masterdata.ProjectionVersion(entity), time.Now().UTC())
		}
		if err != nil {
			return fmt.Errorf("write entity region %s entity %s: %w", region, entity, err)
		}
		return nil
	})
}

func encodeProjection(builder *masterdata.ProjectionBuilder) ([]byte, error) {
	if builder == nil {
		return nil, nil
	}
	body, err := masterdata.EncodeProjection(builder.Build())
	if err != nil {
		return nil, err
	}
	return compressBlob(body), nil
}

func replaceBlocks(ctx context.Context, tx pgx.Tx, region, entity string, blocks []encodedBlock) error {
	if _, err := tx.Exec(ctx, `DELETE FROM master_blocks WHERE region = $1 AND entity = $2`, region, entity); err != nil {
		return fmt.Errorf("delete blocks region %s entity %s: %w", region, entity, err)
	}
	rows := make([][]any, len(blocks))
	for index, block := range blocks {
		rows[index] = []any{region, entity, block.firstKey, block.body}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"master_blocks"}, []string{"region", "entity", "first_key", "body"}, pgx.CopyFromRows(rows)); err != nil {
		return fmt.Errorf("copy blocks region %s entity %s: %w", region, entity, err)
	}
	return nil
}

func replacePostings(ctx context.Context, tx pgx.Tx, region, entity string, indexes map[string]map[string][]string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM master_record_index WHERE region = $1 AND entity = $2`, region, entity); err != nil {
		return fmt.Errorf("delete postings region %s entity %s: %w", region, entity, err)
	}
	rows := make([][]any, 0)
	for name, index := range indexes {
		for key, recordKeys := range index {
			rows = append(rows, []any{region, entity, name, key, recordKeys})
		}
	}
	if len(rows) == 0 {
		return nil
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"master_record_index"}, []string{"region", "entity", "index_name", "index_key", "record_keys"}, pgx.CopyFromRows(rows)); err != nil {
		return fmt.Errorf("copy postings region %s entity %s: %w", region, entity, err)
	}
	return nil
}

func replaceProjection(ctx context.Context, tx pgx.Tx, region, entity string, body []byte) error {
	var err error
	if body == nil {
		_, err = tx.Exec(ctx, `DELETE FROM master_projections WHERE region = $1 AND entity = $2`, region, entity)
	} else {
		_, err = tx.Exec(ctx, `
INSERT INTO master_projections (region, entity, version, body, updated_at) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (region, entity) DO UPDATE SET version = EXCLUDED.version, body = EXCLUDED.body, updated_at = EXCLUDED.updated_at`,
			region, entity, masterdata.ProjectionVersion(entity), body, time.Now().UTC())
	}
	if err != nil {
		return fmt.Errorf("write projection region %s entity %s: %w", region, entity, err)
	}
	return nil
}

// inFencedTx runs fn in one transaction that checks the sync lease before and
// after fn: a write carrying a lease token (masterdata.WithFencingToken) is
// rejected with masterdata.ErrFencedOut unless that token is still current.
// The initial check is lock-free; after fn succeeds, FOR SHARE validates the
// token at the commit boundary and orders a takeover after this transaction.
// Writes without a token (unleased paths) pass.
func (store *PostgresMasterDataStore) inFencedTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		token := masterdata.FencingTokenFromContext(ctx)
		if store.leaseName != "" && token > 0 {
			var leaseToken int64
			err := tx.QueryRow(ctx, `SELECT fencing_token FROM master_data_sync_leases WHERE name = $1`, store.leaseName).Scan(&leaseToken)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && leaseToken != token) {
				return masterdata.ErrFencedOut
			}
			if err != nil {
				return fmt.Errorf("read sync lease for fencing: %w", err)
			}
		}
		if err := fn(tx); err != nil {
			return err
		}
		if store.leaseName != "" && token > 0 {
			var leaseToken int64
			err := tx.QueryRow(ctx, `SELECT fencing_token FROM master_data_sync_leases WHERE name = $1 FOR SHARE`, store.leaseName).Scan(&leaseToken)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && leaseToken != token) {
				return masterdata.ErrFencedOut
			}
			if err != nil {
				return fmt.Errorf("read sync lease for fencing: %w", err)
			}
		}
		return nil
	})
}

// PruneRegionEntities deletes region's entities that keep does not name.
// keep holds payload file paths or entity names. It returns the deleted
// entities, sorted.
func (store *PostgresMasterDataStore) PruneRegionEntities(ctx context.Context, region string, keep []string) ([]string, error) {
	regionName := normalizeKey(region)
	kept := make([]string, 0, len(keep))
	for _, name := range keep {
		if entity := entityNameFromPath(name); entity != "" {
			kept = append(kept, entity)
		}
	}
	if regionName == "" || len(kept) == 0 {
		return nil, nil
	}

	var removed []string
	err := store.inFencedTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
DELETE FROM master_entities WHERE region = $1 AND NOT (entity = ANY($2)) RETURNING entity`, regionName, kept)
		if err != nil {
			return fmt.Errorf("prune entities region %s: %w", regionName, err)
		}
		removed, err = pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return fmt.Errorf("prune entities region %s: %w", regionName, err)
		}
		if len(removed) == 0 {
			return nil
		}
		for _, table := range []string{"master_blocks", "master_record_index", "master_projections"} {
			if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE region = $1 AND entity = ANY($2)`, regionName, removed); err != nil {
				return fmt.Errorf("prune %s region %s: %w", table, regionName, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(removed)
	return removed, nil
}

// EnsureDerivedEntityData rebuilds the relation index postings and list
// projections of region's entities whose stored version is out of date,
// from the entity's blocks. It returns the entities it rebuilt anything for.
func (store *PostgresMasterDataStore) EnsureDerivedEntityData(ctx context.Context, region string) ([]string, error) {
	regionName := normalizeKey(region)
	rebuilt := make([]string, 0)
	for _, entity := range derivedDataEntities() {
		var indexVersion, projectionVersion string
		var recordCount int
		err := store.pool.QueryRow(ctx, `
SELECT index_version, projection_version, record_count FROM master_entities
WHERE region = $1 AND entity = $2`, regionName, entity).Scan(&indexVersion, &projectionVersion, &recordCount)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return rebuilt, fmt.Errorf("read entity region %s entity %s: %w", regionName, entity, err)
		}
		indexesStale := indexVersion != masterdata.IndexVersion(entity)
		projectionStale := projectionVersion != masterdata.ProjectionVersion(entity)
		if recordCount == 0 || (!indexesStale && !projectionStale) {
			continue
		}

		keys, records, err := store.loadEntityInOrder(ctx, regionName, entity)
		if err != nil {
			return rebuilt, err
		}
		var projection []byte
		if projectionStale {
			builder := masterdata.NewProjectionBuilder(entity)
			if builder != nil {
				for position, record := range records {
					if record != nil {
						builder.Add(keys[position], record)
					}
				}
			}
			if projection, err = encodeProjection(builder); err != nil {
				return rebuilt, fmt.Errorf("projection region %s entity %s: %w", regionName, entity, err)
			}
		}

		err = store.inFencedTx(ctx, func(tx pgx.Tx) error {
			if indexesStale {
				indexes := newEntityIndexes(entity)
				for position, record := range records {
					addToEntityIndexes(indexes, keys[position], record)
				}
				if err := replacePostings(ctx, tx, regionName, entity, indexes); err != nil {
					return err
				}
			}
			if projectionStale {
				if err := replaceProjection(ctx, tx, regionName, entity, projection); err != nil {
					return err
				}
			}
			_, err := tx.Exec(ctx, `
UPDATE master_entities SET index_version = $3, projection_version = $4, updated_at = $5
WHERE region = $1 AND entity = $2`,
				regionName, entity, masterdata.IndexVersion(entity), masterdata.ProjectionVersion(entity), time.Now().UTC())
			return err
		})
		if err != nil {
			return rebuilt, fmt.Errorf("write derived data region %s entity %s: %w", regionName, entity, err)
		}
		rebuilt = append(rebuilt, entity)
	}
	return rebuilt, nil
}

func (store *PostgresMasterDataStore) StoreRegionVersionPayload(ctx context.Context, region string, version any) error {
	regionName := normalizeKey(region)
	if regionName == "" {
		return errors.New("region is required")
	}
	body, err := json.Marshal(version)
	if err != nil {
		return fmt.Errorf("marshal version payload for region %s: %w", regionName, err)
	}
	return store.inFencedTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
INSERT INTO master_versions (region, payload, updated_at) VALUES ($1, $2, $3)
ON CONFLICT (region) DO UPDATE SET payload = EXCLUDED.payload, updated_at = EXCLUDED.updated_at`,
			regionName, string(body), time.Now().UTC()); err != nil {
			return fmt.Errorf("store version payload for region %s: %w", regionName, err)
		}
		return nil
	})
}

// --- Reads ------------------------------------------------------------------
//
// Every read that must see one version of an entity is one SQL statement, so
// it sees one snapshot. ListByPage reads the order keys and then the page's
// records; a key a concurrent sync removed in between is skipped.

func (store *PostgresMasterDataStore) LoadRegionVersionPayload(ctx context.Context, region string) (any, bool, error) {
	regionName := normalizeKey(region)
	if regionName == "" {
		return nil, false, nil
	}
	var body []byte
	err := store.pool.QueryRow(ctx, `SELECT payload::text FROM master_versions WHERE region = $1`, regionName).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load version payload for region %s: %w", regionName, err)
	}
	var version map[string]any
	if err := json.Unmarshal(body, &version); err != nil {
		return nil, false, fmt.Errorf("unmarshal version payload for region %s: %w", regionName, err)
	}
	return version, true, nil
}

func (store *PostgresMasterDataStore) HasEntityRecords(ctx context.Context, region string, entity string) (bool, error) {
	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if regionName == "" || entityName == "" {
		return false, nil
	}
	var hasRecords bool
	err := store.pool.QueryRow(ctx, `
SELECT record_count > 0 FROM master_entities WHERE region = $1 AND entity = $2`, regionName, entityName).Scan(&hasRecords)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check records region %s entity %s: %w", regionName, entityName, err)
	}
	return hasRecords, nil
}

// HasRegionData reports whether any entity of region has records.
func (store *PostgresMasterDataStore) HasRegionData(ctx context.Context, region string) (bool, error) {
	regionName := normalizeKey(region)
	if regionName == "" {
		return false, nil
	}
	var hasData bool
	if err := store.pool.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM master_entities WHERE region = $1 AND record_count > 0)`, regionName).Scan(&hasData); err != nil {
		return false, fmt.Errorf("check region data %s: %w", regionName, err)
	}
	return hasData, nil
}

// RegionRecordCounts returns the number of stored records per region, for
// regions with at least one entity row. It is one aggregate over
// master_entities, cheap enough for a metrics callback.
func (store *PostgresMasterDataStore) RegionRecordCounts(ctx context.Context) (map[string]int64, error) {
	rows, err := store.pool.Query(ctx, `
SELECT region, COALESCE(SUM(record_count), 0)::bigint FROM master_entities GROUP BY region`)
	if err != nil {
		return nil, fmt.Errorf("count region records: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int64)
	for rows.Next() {
		var region string
		var count int64
		if err := rows.Scan(&region, &count); err != nil {
			return nil, fmt.Errorf("count region records: %w", err)
		}
		counts[region] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("count region records: %w", err)
	}
	return counts, nil
}

func (store *PostgresMasterDataStore) GetByID(ctx context.Context, region string, entity string, id string) (_ map[string]any, _ bool, err error) {
	ctx, span := tracing.StartSpan(ctx, "postgres.master_data.get_by_id", attribute.String("region", normalizeKey(region)), attribute.String("entity", normalizeKey(entity)))
	defer func() { tracing.EndSpan(span, err) }()

	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	key := strings.TrimSpace(id)
	if regionName == "" || entityName == "" || key == "" || masterdata.UsesCompositeKey(entityName) {
		return nil, false, nil
	}
	records, err := store.fetchRecords(ctx, regionName, entityName, []string{key})
	if err != nil {
		return nil, false, err
	}
	return records[0], records[0] != nil, nil
}

// GetByIDs returns the records stored under ids, aligned with ids, with nil
// for an ID without a record. A bare ID never matches a record of a
// composite-key entity.
func (store *PostgresMasterDataStore) GetByIDs(ctx context.Context, region string, entity string, ids []string) (_ []map[string]any, err error) {
	ctx, span := tracing.StartSpan(ctx, "postgres.master_data.get_by_ids", attribute.String("region", normalizeKey(region)), attribute.String("entity", normalizeKey(entity)))
	defer func() { tracing.EndSpan(span, err) }()

	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	records := make([]map[string]any, len(ids))
	if regionName == "" || entityName == "" || len(ids) == 0 || masterdata.UsesCompositeKey(entityName) {
		return records, nil
	}
	keys := make([]string, len(ids))
	for index, id := range ids {
		keys[index] = strings.TrimSpace(id)
	}
	return store.fetchRecords(ctx, regionName, entityName, keys)
}

// GetByCompositeKeys returns the records of a composite-key entity whose key
// fields equal each key, aligned with keys, with nil for a key missing a key
// field or without a record.
func (store *PostgresMasterDataStore) GetByCompositeKeys(ctx context.Context, region string, entity string, keys []map[string]any) (_ []map[string]any, err error) {
	ctx, span := tracing.StartSpan(ctx, "postgres.master_data.get_by_composite_keys", attribute.String("region", normalizeKey(region)), attribute.String("entity", normalizeKey(entity)))
	defer func() { tracing.EndSpan(span, err) }()

	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	records := make([]map[string]any, len(keys))
	if regionName == "" || entityName == "" || len(keys) == 0 || !masterdata.UsesCompositeKey(entityName) {
		return records, nil
	}
	recordKeys := make([]string, len(keys))
	for index, key := range keys {
		if recordKey, ok := masterdata.CompositeRecordKey(entityName, key); ok {
			recordKeys[index] = recordKey
		}
	}
	return store.fetchRecords(ctx, regionName, entityName, recordKeys)
}

func (store *PostgresMasterDataStore) ListAll(ctx context.Context, region string, entity string) (_ []map[string]any, err error) {
	ctx, span := tracing.StartSpan(ctx, "postgres.master_data.list_all", attribute.String("region", normalizeKey(region)), attribute.String("entity", normalizeKey(entity)))
	defer func() { tracing.EndSpan(span, err) }()

	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if regionName == "" || entityName == "" {
		return []map[string]any{}, nil
	}
	_, records, err := store.loadEntityInOrder(ctx, regionName, entityName)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if record != nil {
			items = append(items, record)
		}
	}
	return items, nil
}

func (store *PostgresMasterDataStore) ListByPage(ctx context.Context, region string, entity string, page int, pageSize int) (_ []map[string]any, _ int, err error) {
	ctx, span := tracing.StartSpan(ctx, "postgres.master_data.list_by_page", attribute.String("region", normalizeKey(region)), attribute.String("entity", normalizeKey(entity)))
	defer func() { tracing.EndSpan(span, err) }()

	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if regionName == "" || entityName == "" {
		return []map[string]any{}, 0, nil
	}

	var total int
	var orderBody []byte
	err = store.pool.QueryRow(ctx, `
SELECT record_count, order_keys FROM master_entities WHERE region = $1 AND entity = $2`, regionName, entityName).Scan(&total, &orderBody)
	if errors.Is(err, pgx.ErrNoRows) {
		return []map[string]any{}, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read order keys region %s entity %s: %w", regionName, entityName, err)
	}
	if total <= 0 {
		return []map[string]any{}, 0, nil
	}
	start := (page - 1) * pageSize
	if start >= total {
		return []map[string]any{}, total, nil
	}
	orderKeys, err := decodeOrderKeys(orderBody)
	if err != nil {
		return nil, 0, fmt.Errorf("order keys region %s entity %s: %w", regionName, entityName, err)
	}
	end := min(start+pageSize, len(orderKeys))
	if start >= end {
		return []map[string]any{}, total, nil
	}

	records, err := store.fetchRecords(ctx, regionName, entityName, orderKeys[start:end])
	if err != nil {
		return nil, 0, err
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if record != nil {
			items = append(items, record)
		}
	}
	return items, total, nil
}

// listByIndexQuery reads, in one statement, the entity's index version and
// record count (kind 0), the postings of the looked-up keys (kind 1) and,
// when the index is current, the blocks holding every posted record (kind 2).
const listByIndexQuery = `
WITH entity_row AS (
	SELECT index_version, record_count FROM master_entities WHERE region = $1 AND entity = $2
), postings AS (
	SELECT index_key, record_keys FROM master_record_index
	WHERE region = $1 AND entity = $2 AND index_name = $3 AND index_key = ANY($4::text[])
		AND EXISTS (SELECT 1 FROM entity_row WHERE index_version = $5)
), block_keys AS (
	SELECT DISTINCT (
		SELECT b.first_key FROM master_blocks b
		WHERE b.region = $1 AND b.entity = $2 AND b.first_key <= master_block_sort_key(k.record_key)
		ORDER BY b.first_key DESC LIMIT 1
	) AS first_key
	FROM postings, unnest(postings.record_keys) AS k(record_key)
)
SELECT 0 AS kind, e.index_version, e.record_count, NULL::text AS index_key, NULL::text[] AS record_keys, NULL::bytea AS body
FROM entity_row e
UNION ALL
SELECT 1, NULL::text, NULL::integer, p.index_key, p.record_keys, NULL::bytea FROM postings p
UNION ALL
SELECT 2, NULL::text, NULL::integer, NULL::text, NULL::text[], b.body FROM master_blocks b
WHERE b.region = $1 AND b.entity = $2 AND b.first_key IN (SELECT first_key FROM block_keys)`

// ListByIndex returns, for each lookup, the records whose index fields equal
// the lookup's values (in the index's field order), in stored order. Until
// the index is built for the stored records, the entity is scanned instead.
func (store *PostgresMasterDataStore) ListByIndex(ctx context.Context, region string, entity string, index string, lookups [][]any) (_ [][]map[string]any, err error) {
	ctx, span := tracing.StartSpan(ctx, "postgres.master_data.list_by_index", attribute.String("region", normalizeKey(region)), attribute.String("entity", normalizeKey(entity)))
	defer func() { tracing.EndSpan(span, err) }()

	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if !masterdata.HasIndex(entityName, index) {
		return nil, fmt.Errorf("%w: %s on %s", ErrUnknownIndex, index, entityName)
	}

	results := make([][]map[string]any, len(lookups))
	keys := make([]string, 0, len(lookups))
	positions := make([]int, 0, len(lookups))
	for position, lookup := range lookups {
		if key, ok := masterdata.IndexLookupKey(lookup...); ok {
			keys = append(keys, key)
			positions = append(positions, position)
		}
	}
	if len(keys) == 0 {
		return results, nil
	}

	rows, err := store.pool.Query(ctx, listByIndexQuery, regionName, entityName, index, keys, masterdata.IndexVersion(entityName))
	if err != nil {
		return nil, fmt.Errorf("read index %s region %s entity %s: %w", index, regionName, entityName, err)
	}
	var entityFound bool
	var indexVersion string
	var recordCount int
	postings := make(map[string][]string)
	var bodies [][]byte
	for rows.Next() {
		var kind int
		var version, indexKey *string
		var count *int32
		var recordKeys []string
		var body []byte
		if err := rows.Scan(&kind, &version, &count, &indexKey, &recordKeys, &body); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan index %s region %s entity %s: %w", index, regionName, entityName, err)
		}
		switch kind {
		case 0:
			entityFound = true
			indexVersion = *version
			recordCount = int(*count)
		case 1:
			postings[*indexKey] = recordKeys
		case 2:
			bodies = append(bodies, body)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read index %s region %s entity %s: %w", index, regionName, entityName, err)
	}

	if !entityFound || recordCount == 0 {
		// A region without this entity (TW has no musiccategories) has no
		// records and so no index to build.
		for _, position := range positions {
			results[position] = make([]map[string]any, 0)
		}
		return results, nil
	}
	if indexVersion != masterdata.IndexVersion(entityName) {
		warnMasterDataScan(ctx, "master data index not built; scanning entity", regionName, entityName, index)
		return store.scanByIndex(ctx, regionName, entityName, index, keys, positions, results)
	}

	raws, err := blockRecordsByKey(bodies)
	if err != nil {
		return nil, fmt.Errorf("index %s region %s entity %s: %w", index, regionName, entityName, err)
	}
	unique := make([]string, 0)
	seen := make(map[string]struct{})
	for _, key := range keys {
		for _, recordKey := range postings[key] {
			if _, duplicate := seen[recordKey]; !duplicate && raws[recordKey] != nil {
				seen[recordKey] = struct{}{}
				unique = append(unique, recordKey)
			}
		}
	}
	uniqueRaws := make([]json.RawMessage, len(unique))
	for position, recordKey := range unique {
		uniqueRaws[position] = raws[recordKey]
	}
	decoded, err := decodeRecords(uniqueRaws)
	if err != nil {
		return nil, fmt.Errorf("index %s region %s entity %s: %w", index, regionName, entityName, err)
	}
	records := make(map[string]map[string]any, len(unique))
	for position, recordKey := range unique {
		records[recordKey] = decoded[position]
	}

	for position, key := range keys {
		matched := make([]map[string]any, 0, len(postings[key]))
		for _, recordKey := range postings[key] {
			if record := records[recordKey]; record != nil {
				matched = append(matched, record)
			}
		}
		results[positions[position]] = matched
	}
	return results, nil
}

func (store *PostgresMasterDataStore) scanByIndex(ctx context.Context, region, entity, index string, keys []string, positions []int, results [][]map[string]any) ([][]map[string]any, error) {
	records, err := store.ListAll(ctx, region, entity)
	if err != nil {
		return nil, err
	}
	wanted := make(map[string][]int, len(keys))
	for position, key := range keys {
		wanted[key] = append(wanted[key], positions[position])
	}
	for position := range keys {
		results[positions[position]] = make([]map[string]any, 0)
	}
	for _, record := range records {
		for _, key := range masterdata.IndexKeys(record, index) {
			for _, position := range wanted[key] {
				results[position] = append(results[position], record)
			}
		}
	}
	return results, nil
}

// LoadProjection returns entity's list projection. A region without the
// entity gets an empty projection. Until sync has built the projection for
// the stored records, it is built from them.
func (store *PostgresMasterDataStore) LoadProjection(ctx context.Context, region string, entity string) (_ *masterdata.Projection, err error) {
	ctx, span := tracing.StartSpan(ctx, "postgres.master_data.load_projection", attribute.String("region", normalizeKey(region)), attribute.String("entity", normalizeKey(entity)))
	defer func() { tracing.EndSpan(span, err) }()

	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if masterdata.ProjectionVersion(entityName) == "" {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProjection, entityName)
	}

	var recordCount int
	var version *string
	var body []byte
	err = store.pool.QueryRow(ctx, `
SELECT e.record_count, p.version, p.body FROM master_entities e
LEFT JOIN master_projections p ON p.region = e.region AND p.entity = e.entity
WHERE e.region = $1 AND e.entity = $2`, regionName, entityName).Scan(&recordCount, &version, &body)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && recordCount == 0) {
		return masterdata.BuildProjection(entityName, nil, nil), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read projection region %s entity %s: %w", regionName, entityName, err)
	}
	if version == nil || *version != masterdata.ProjectionVersion(entityName) {
		warnMasterDataScan(ctx, "master data projection not built; building from records", regionName, entityName, "")
		keys, records, err := store.loadEntityInOrder(ctx, regionName, entityName)
		if err != nil {
			return nil, err
		}
		return masterdata.BuildProjection(entityName, keys, records), nil
	}

	plain, err := decompressBlob(body)
	if err != nil {
		return nil, fmt.Errorf("decompress projection region %s entity %s: %w", regionName, entityName, err)
	}
	projection, err := masterdata.DecodeProjection(plain)
	if err != nil {
		return nil, fmt.Errorf("projection region %s entity %s: %w", regionName, entityName, err)
	}
	return projection, nil
}

// fetchRecords reads the records stored under keys in one statement and
// returns them aligned with keys, with nil for a key without a record. Each
// key is located through the block with the greatest first_key at or below
// its sort key.
func (store *PostgresMasterDataStore) fetchRecords(ctx context.Context, region, entity string, keys []string) ([]map[string]any, error) {
	sortKeys := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		sortKey := masterdata.BlockSortKey(key)
		if _, duplicate := seen[sortKey]; !duplicate {
			seen[sortKey] = struct{}{}
			sortKeys = append(sortKeys, sortKey)
		}
	}
	if len(sortKeys) == 0 {
		return make([]map[string]any, len(keys)), nil
	}

	rows, err := store.pool.Query(ctx, `
SELECT b.body FROM master_blocks b
WHERE b.region = $1 AND b.entity = $2 AND b.first_key IN (
	SELECT (
		SELECT p.first_key FROM master_blocks p
		WHERE p.region = $1 AND p.entity = $2 AND p.first_key <= k.sort_key
		ORDER BY p.first_key DESC LIMIT 1
	)
	FROM unnest($3::text[]) AS k(sort_key)
)`, region, entity, sortKeys)
	if err != nil {
		return nil, fmt.Errorf("read blocks region %s entity %s: %w", region, entity, err)
	}
	bodies, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		return nil, fmt.Errorf("read blocks region %s entity %s: %w", region, entity, err)
	}

	raws, err := blockRecordsByKey(bodies)
	if err != nil {
		return nil, fmt.Errorf("region %s entity %s: %w", region, entity, err)
	}
	aligned := make([]json.RawMessage, len(keys))
	for index, key := range keys {
		aligned[index] = raws[key]
	}
	records, err := decodeRecords(aligned)
	if err != nil {
		return nil, fmt.Errorf("region %s entity %s: %w", region, entity, err)
	}
	return records, nil
}

// loadEntityInOrder reads all of an entity's blocks in one statement and
// returns its record keys and records in source order.
func (store *PostgresMasterDataStore) loadEntityInOrder(ctx context.Context, region, entity string) ([]string, []map[string]any, error) {
	rows, err := store.pool.Query(ctx, `SELECT body FROM master_blocks WHERE region = $1 AND entity = $2`, region, entity)
	if err != nil {
		return nil, nil, fmt.Errorf("read blocks region %s entity %s: %w", region, entity, err)
	}
	bodies, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		return nil, nil, fmt.Errorf("read blocks region %s entity %s: %w", region, entity, err)
	}
	blocks, err := decodeBlocks(bodies)
	if err != nil {
		return nil, nil, fmt.Errorf("region %s entity %s: %w", region, entity, err)
	}

	total := 0
	for _, entries := range blocks {
		for _, entry := range entries {
			total += len(entry.positions)
		}
	}
	keys := make([]string, total)
	raws := make([]json.RawMessage, total)
	for _, entries := range blocks {
		for _, entry := range entries {
			for _, position := range entry.positions {
				if position < 0 || position >= total {
					return nil, nil, fmt.Errorf("region %s entity %s: block position %d out of range", region, entity, position)
				}
				keys[position] = entry.key
				raws[position] = entry.record
			}
		}
	}
	records, err := decodeRecords(raws)
	if err != nil {
		return nil, nil, fmt.Errorf("region %s entity %s: %w", region, entity, err)
	}
	return keys, records, nil
}

// blockRecordsByKey decodes block bodies into a map from record key to its
// stored JSON.
func blockRecordsByKey(bodies [][]byte) (map[string]json.RawMessage, error) {
	blocks, err := decodeBlocks(bodies)
	if err != nil {
		return nil, err
	}
	raws := make(map[string]json.RawMessage)
	for _, entries := range blocks {
		for _, entry := range entries {
			raws[entry.key] = entry.record
		}
	}
	return raws, nil
}

func warnMasterDataScan(ctx context.Context, message, region, entity, index string) {
	fields := []any{"region", region, "entity", entity}
	if index != "" {
		fields = append(fields, "index", index)
	}
	logging.FromContext(ctx).Warnw(message, fields...)
}
