package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"sekai-master-api/internal/domain/masterdata"
)

func newPostgresStoreForTest(t *testing.T, leaseName string) (*PostgresMasterDataStore, *DB) {
	t.Helper()
	db := openMigratedTestDB(t)
	return NewPostgresMasterDataStore(db.Pool, 4, leaseName), db
}

func TestPostgresStoreBlocksSplitByCountAndBytes(t *testing.T) {
	store, db := newPostgresStoreForTest(t, "")
	ctx := context.Background()

	records := make([]string, 0, 100)
	for id := 100; id >= 1; id-- {
		body := "small"
		if id > 90 {
			body = strings.Repeat("y", 20000)
		}
		records = append(records, fmt.Sprintf(`{"id":%d,"body":%q}`, id, body))
	}
	if err := store.StoreRegion(ctx, "jp", map[string]any{"cards.json": rawRecords(t, records...)}); err != nil {
		t.Fatalf("store: %v", err)
	}

	rows, err := db.Pool.Query(ctx, `SELECT first_key, body FROM master_blocks WHERE region = 'jp' AND entity = 'cards' ORDER BY first_key`)
	if err != nil {
		t.Fatalf("query blocks: %v", err)
	}
	type block struct {
		FirstKey string
		Body     []byte
	}
	blocks, err := pgx.CollectRows(rows, pgx.RowToStructByPos[block])
	if err != nil {
		t.Fatalf("collect blocks: %v", err)
	}

	var sizes []int
	previousLast := ""
	for _, stored := range blocks {
		entries, err := decodeBlock(stored.Body)
		if err != nil {
			t.Fatalf("decode block: %v", err)
		}
		if len(entries) == 0 || len(entries) > masterBlockMaxRecords {
			t.Fatalf("block %s holds %d records", stored.FirstKey, len(entries))
		}
		if masterdata.BlockSortKey(entries[0].key) != stored.FirstKey {
			t.Fatalf("block first_key %s, first record %s", stored.FirstKey, entries[0].key)
		}
		for _, entry := range entries {
			sortKey := masterdata.BlockSortKey(entry.key)
			if sortKey <= previousLast {
				t.Fatalf("record %s out of order", entry.key)
			}
			previousLast = sortKey
		}
		sizes = append(sizes, len(entries))
	}
	// Blocks close at 32 records, or on the record that brings them to 64 kB
	// of JSON: ids 1-32 and 33-64; 65-94 (91-94 carry 20 kB each); 95-98;
	// 99-100.
	if fmt.Sprint(sizes) != "[32 32 30 4 2]" {
		t.Fatalf("block sizes = %v", sizes)
	}

	var recordCount int
	var orderBody []byte
	if err := db.Pool.QueryRow(ctx, `SELECT record_count, order_keys FROM master_entities WHERE region = 'jp' AND entity = 'cards'`).Scan(&recordCount, &orderBody); err != nil {
		t.Fatalf("read entity: %v", err)
	}
	orderKeys, err := decodeOrderKeys(orderBody)
	if err != nil || recordCount != 100 || orderKeys[0] != "100" || orderKeys[99] != "1" {
		t.Fatalf("order keys = %d keys (%v…), count %d, err %v", len(orderKeys), orderKeys[:2], recordCount, err)
	}
}

func TestPostgresStoreFencesWritesByLeaseToken(t *testing.T) {
	store, db := newPostgresStoreForTest(t, "master-data-sync")
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `
INSERT INTO master_data_sync_leases (name, holder, fencing_token, acquired_at, expires_at, last_heartbeat_at)
VALUES ('master-data-sync', 'holder-b', 5, now(), now() + interval '1 minute', now())`); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	payload := func(prefix string) map[string]any {
		return map[string]any{"cards.json": rawRecords(t, fmt.Sprintf(`{"id":1,"prefix":%q}`, prefix))}
	}
	prefix := func() any {
		record, _, err := store.GetByID(ctx, "jp", "cards", "1")
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return record["prefix"]
	}

	if err := store.StoreRegion(masterdata.WithFencingToken(ctx, 5), "jp", payload("current owner")); err != nil {
		t.Fatalf("current-token write: %v", err)
	}
	if err := store.StoreRegion(masterdata.WithFencingToken(ctx, 4), "jp", payload("stale owner")); !errors.Is(err, masterdata.ErrFencedOut) {
		t.Fatalf("stale-token write error = %v, want ErrFencedOut", err)
	}
	if err := store.StoreRegionVersionPayload(masterdata.WithFencingToken(ctx, 4), "jp", map[string]any{"dataVersion": "stale"}); !errors.Is(err, masterdata.ErrFencedOut) {
		t.Fatalf("stale-token version write error = %v, want ErrFencedOut", err)
	}
	if _, err := store.PruneRegionEntities(masterdata.WithFencingToken(ctx, 4), "jp", []string{"gachas.json"}); !errors.Is(err, masterdata.ErrFencedOut) {
		t.Fatalf("stale-token prune error = %v, want ErrFencedOut", err)
	}
	if got := prefix(); got != "current owner" {
		t.Fatalf("record after fenced writes = %v", got)
	}

	// Unleased writes (no token) pass, as they do for sync status.
	if err := store.StoreRegion(ctx, "jp", payload("unleased")); err != nil {
		t.Fatalf("unleased write: %v", err)
	}
	if got := prefix(); got != "unleased" {
		t.Fatalf("record after unleased write = %v", got)
	}

	// A token for a lease row that does not exist is fenced out.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM master_data_sync_leases`); err != nil {
		t.Fatalf("delete lease: %v", err)
	}
	if err := store.StoreRegion(masterdata.WithFencingToken(ctx, 5), "jp", payload("no lease")); !errors.Is(err, masterdata.ErrFencedOut) {
		t.Fatalf("write without lease row error = %v, want ErrFencedOut", err)
	}
}

func TestPostgresStoreLeaseRenewalDoesNotWaitForEntityTransaction(t *testing.T) {
	store, db := newPostgresStoreForTest(t, "master-data-sync")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	seedFencingLease(t, ctx, db, "master-data-sync", "holder-a", 1)

	write := startPausedFencedWrite(t, ctx, store, 1, "renewal-test", `{"write":"uncommitted"}`)
	select {
	case <-write.callbackStarted:
	case <-ctx.Done():
		t.Fatalf("callback did not reach its pause: %v", ctx.Err())
	}

	renewCtx, cancelRenew := context.WithTimeout(ctx, 3*time.Second)
	defer cancelRenew()
	renewDone := make(chan error, 1)
	go func() {
		now := time.Now().UTC()
		tag, err := db.Pool.Exec(renewCtx,
			`UPDATE master_data_sync_leases SET expires_at = $3, last_heartbeat_at = $4 WHERE name = $1 AND holder = $2`,
			"master-data-sync", "holder-a", now.Add(time.Minute), now)
		if err == nil && tag.RowsAffected() != 1 {
			err = fmt.Errorf("renew updated %d rows, want 1", tag.RowsAffected())
		}
		renewDone <- err
	}()
	select {
	case err := <-renewDone:
		if err != nil {
			t.Fatalf("renew while callback is paused: %v", err)
		}
	case <-renewCtx.Done():
		t.Fatalf("renew did not complete while callback was paused: %v", renewCtx.Err())
	}
	select {
	case err := <-write.writeErr:
		t.Fatalf("transaction returned before callback was released: %v", err)
	default:
	}

	write.releaseCallback()
	select {
	case err := <-write.writeErr:
		if err != nil {
			t.Fatalf("fenced transaction after renewal: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("fenced transaction did not finish: %v", ctx.Err())
	}
	assertMasterVersionCount(t, ctx, db, "renewal-test", 1)
}

func TestPostgresStoreRejectsTakeoverDuringCallbackAndRollsBack(t *testing.T) {
	store, db := newPostgresStoreForTest(t, "master-data-sync")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	seedFencingLease(t, ctx, db, "master-data-sync", "holder-a", 1)
	write := startPausedFencedWrite(t, ctx, store, 1, "takeover-test", `{"write":"must rollback"}`)
	select {
	case <-write.callbackStarted:
	case <-ctx.Done():
		t.Fatalf("callback did not reach its pause: %v", ctx.Err())
	}
	assertMasterVersionCount(t, ctx, db, "takeover-test", 0)

	if tag, err := db.Pool.Exec(ctx, `UPDATE master_data_sync_leases SET expires_at = now() - interval '1 second' WHERE name = $1`, "master-data-sync"); err != nil {
		t.Fatalf("expire lease for takeover: %v", err)
	} else if tag.RowsAffected() != 1 {
		t.Fatalf("expire lease updated %d rows, want 1", tag.RowsAffected())
	}
	if token, acquired, err := acquireSyncLeaseForTest(ctx, db.Pool.QueryRow, "master-data-sync", "holder-b", time.Minute); err != nil || !acquired || token != 2 {
		t.Fatalf("takeover = token %d, acquired %t, error %v; want token 2", token, acquired, err)
	}

	write.releaseCallback()
	select {
	case err := <-write.writeErr:
		if !errors.Is(err, masterdata.ErrFencedOut) {
			t.Fatalf("stale callback transaction error = %v, want ErrFencedOut", err)
		}
	case <-ctx.Done():
		t.Fatalf("stale callback transaction did not finish: %v", ctx.Err())
	}
	assertMasterVersionCount(t, ctx, db, "takeover-test", 0)
	assertFencingLease(t, ctx, db, "master-data-sync", "holder-b", 2)
}

type pausedFencedWrite struct {
	callbackStarted chan struct{}
	releaseCallback func()
	writeErr        chan error
}

func startPausedFencedWrite(t *testing.T, ctx context.Context, store *PostgresMasterDataStore, token int64, region, payload string) pausedFencedWrite {
	t.Helper()
	callbackStarted := make(chan struct{})
	releaseCallback := make(chan struct{})
	callbackReleased := false
	release := func() {
		if !callbackReleased {
			close(releaseCallback)
			callbackReleased = true
		}
	}
	t.Cleanup(release)
	writeErr := make(chan error, 1)
	go func() {
		writeErr <- store.inFencedTx(masterdata.WithFencingToken(ctx, token), func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO master_versions (region, payload, updated_at) VALUES ($1, $2, $3)`, region, payload, time.Now().UTC()); err != nil {
				return err
			}
			close(callbackStarted)
			select {
			case <-releaseCallback:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	return pausedFencedWrite{
		callbackStarted: callbackStarted,
		releaseCallback: release,
		writeErr:        writeErr,
	}
}

func TestPostgresStoreFinalFenceLockOrdersTakeoverAfterCommit(t *testing.T) {
	store, db := newPostgresStoreForTest(t, "master-data-sync")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const advisoryKey1, advisoryKey2 int32 = 7139, 90210
	seedFencingLease(t, ctx, db, "master-data-sync", "holder-a", 1)
	if _, err := db.Pool.Exec(ctx, `UPDATE master_data_sync_leases SET expires_at = now() - interval '1 second' WHERE name = $1`, "master-data-sync"); err != nil {
		t.Fatalf("expire lease before commit-order test: %v", err)
	}

	if _, err := db.Pool.Exec(ctx, `
CREATE FUNCTION test_hold_master_version_commit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	PERFORM pg_advisory_xact_lock(7139, 90210);
	RETURN NULL;
END;
$$`); err != nil {
		t.Fatalf("create deferred commit trigger function: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `
CREATE CONSTRAINT TRIGGER test_hold_master_version_commit
AFTER INSERT ON master_versions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION test_hold_master_version_commit()`); err != nil {
		t.Fatalf("create deferred commit trigger: %v", err)
	}
	lockConn, err := db.Pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire advisory lock connection: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		_, _ = lockConn.Exec(cleanupCtx, `SELECT pg_advisory_unlock($1, $2)`, advisoryKey1, advisoryKey2)
		lockConn.Release()
	}()
	if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_lock($1, $2)`, advisoryKey1, advisoryKey2); err != nil {
		t.Fatalf("hold advisory lock for deferred trigger: %v", err)
	}

	backendPID := make(chan int32, 1)
	writeErr := make(chan error, 1)
	go func() {
		writeErr <- store.inFencedTx(masterdata.WithFencingToken(ctx, 1), func(tx pgx.Tx) error {
			var pid int32
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				return err
			}
			backendPID <- pid
			_, err := tx.Exec(ctx, `INSERT INTO master_versions (region, payload, updated_at) VALUES ($1, $2, $3)`, "commit-order-test", `{"write":"committed before takeover"}`, time.Now().UTC())
			return err
		})
	}()
	var writerPID int32
	select {
	case writerPID = <-backendPID:
	case <-ctx.Done():
		t.Fatalf("fenced transaction did not start: %v", ctx.Err())
	}
	if err := waitForPostgresWaitEvent(ctx, db.Pool, writerPID, "Lock", "advisory"); err != nil {
		t.Fatalf("transaction did not reach its deferred commit trigger after final fencing validation: %v", err)
	}

	acquireConn, err := db.Pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire takeover connection: %v", err)
	}
	defer acquireConn.Release()
	var acquirePID int32
	if err := acquireConn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&acquirePID); err != nil {
		t.Fatalf("read takeover backend pid: %v", err)
	}
	type acquireResult struct {
		token    int64
		acquired bool
		err      error
	}
	acquireDone := make(chan acquireResult, 1)
	go func() {
		token, acquired, err := acquireSyncLeaseForTest(ctx, acquireConn.QueryRow, "master-data-sync", "holder-b", time.Minute)
		acquireDone <- acquireResult{token: token, acquired: acquired, err: err}
	}()
	if err := waitForPostgresBlockingPID(ctx, db.Pool, acquirePID, writerPID); err != nil {
		t.Fatalf("takeover was not blocked by the final shared lease-row lock: %v", err)
	}

	if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_unlock($1, $2)`, advisoryKey1, advisoryKey2); err != nil {
		t.Fatalf("release advisory lock: %v", err)
	}
	select {
	case err := <-writeErr:
		if err != nil {
			t.Fatalf("valid fenced transaction: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("valid fenced transaction did not commit: %v", ctx.Err())
	}
	select {
	case result := <-acquireDone:
		if result.err != nil || !result.acquired || result.token != 2 {
			t.Fatalf("takeover after commit = token %d, acquired %t, error %v; want token 2", result.token, result.acquired, result.err)
		}
	case <-ctx.Done():
		t.Fatalf("takeover did not finish after commit: %v", ctx.Err())
	}
	assertMasterVersionCount(t, ctx, db, "commit-order-test", 1)
	assertFencingLease(t, ctx, db, "master-data-sync", "holder-b", 2)
}

func seedFencingLease(t *testing.T, ctx context.Context, db *DB, name, holder string, token int64) {
	t.Helper()
	if _, err := db.Pool.Exec(ctx, `
INSERT INTO master_data_sync_leases (name, holder, fencing_token, acquired_at, expires_at, last_heartbeat_at)
VALUES ($1, $2, $3, now(), now() + interval '1 minute', now())`, name, holder, token); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
}

func acquireSyncLeaseForTest(ctx context.Context, queryRow func(context.Context, string, ...any) pgx.Row, name, holder string, ttl time.Duration) (int64, bool, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(ttl)
	var token int64
	var acquiredAt, storedExpiresAt, lastHeartbeatAt time.Time
	err := queryRow(ctx, `
INSERT INTO master_data_sync_leases (
	name, holder, fencing_token, acquired_at, expires_at, last_heartbeat_at
) VALUES ($1, $2, 1, $3, $4, $5)
ON CONFLICT (name) DO UPDATE SET
	holder = EXCLUDED.holder,
	fencing_token = master_data_sync_leases.fencing_token + 1,
	acquired_at = EXCLUDED.acquired_at,
	expires_at = EXCLUDED.expires_at,
	last_heartbeat_at = EXCLUDED.last_heartbeat_at
WHERE master_data_sync_leases.expires_at <= EXCLUDED.last_heartbeat_at
RETURNING fencing_token, acquired_at, expires_at, last_heartbeat_at`, name, holder, now, expiresAt, now).Scan(
		&token, &acquiredAt, &storedExpiresAt, &lastHeartbeatAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return token, err == nil, err
}

func assertMasterVersionCount(t *testing.T, ctx context.Context, db *DB, region string, want int) {
	t.Helper()
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM master_versions WHERE region = $1`, region).Scan(&count); err != nil {
		t.Fatalf("count master version rows: %v", err)
	}
	if count != want {
		t.Fatalf("master version rows for %s = %d, want %d", region, count, want)
	}
}

func assertFencingLease(t *testing.T, ctx context.Context, db *DB, name, holder string, wantToken int64) {
	t.Helper()
	var gotHolder string
	var gotToken int64
	if err := db.Pool.QueryRow(ctx, `SELECT holder, fencing_token FROM master_data_sync_leases WHERE name = $1`, name).Scan(&gotHolder, &gotToken); err != nil {
		t.Fatalf("read fencing lease: %v", err)
	}
	if gotHolder != holder || gotToken != wantToken {
		t.Fatalf("lease holder/token = %s/%d, want %s/%d", gotHolder, gotToken, holder, wantToken)
	}
}

func waitForPostgresWaitEvent(ctx context.Context, pool *pgxpool.Pool, pid int32, wantType, wantEvent string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waitType, waitEvent *string
		err := pool.QueryRow(ctx, `SELECT wait_event_type, wait_event FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&waitType, &waitEvent)
		if err == nil && waitType != nil && waitEvent != nil && *waitType == wantType && *waitEvent == wantEvent {
			return nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitForPostgresBlockingPID(ctx context.Context, pool *pgxpool.Pool, pid, wantBlocker int32) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		err := pool.QueryRow(ctx, `
SELECT EXISTS (
	SELECT 1 FROM unnest(pg_blocking_pids($1)) AS blocker(pid) WHERE pid = $2
)`, pid, wantBlocker).Scan(&blocked)
		if err != nil {
			return err
		}
		if blocked {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func TestPostgresStoreWithoutLeaseNameDoesNotFence(t *testing.T) {
	store, _ := newPostgresStoreForTest(t, "")
	if err := store.StoreRegion(masterdata.WithFencingToken(context.Background(), 9), "jp", map[string]any{"cards.json": rawRecords(t, `{"id":1}`)}); err != nil {
		t.Fatalf("write with fencing disabled: %v", err)
	}
}

func TestPostgresStorePrunesEntitiesTheSourceDropped(t *testing.T) {
	store, db := newPostgresStoreForTest(t, "")
	ctx := context.Background()
	storeContractPayload(t, store)
	if err := store.StoreRegion(ctx, "en", map[string]any{"gachas.json": contractPayload(t)["gachas.json"]}); err != nil {
		t.Fatalf("store en: %v", err)
	}

	removed, err := store.PruneRegionEntities(ctx, "jp", []string{"master/cards.json", "cardEpisodes.json", "resourceBoxes.json", "resourceBoxDetails.json", "musicVocals.json", "versions.json"})
	if err != nil || strings.Join(removed, ",") != "gachas" {
		t.Fatalf("removed = %v %v", removed, err)
	}
	for _, table := range []string{"master_entities", "master_blocks", "master_record_index", "master_projections"} {
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE region = 'jp' AND entity = 'gachas'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s keeps %d gachas rows (%v)", table, count, err)
		}
	}
	if gachas, err := store.ListAll(ctx, "en", "gachas"); err != nil || len(gachas) != 2 {
		t.Fatalf("other region's gachas = %d %v", len(gachas), err)
	}
	if cards, err := store.ListAll(ctx, "jp", "cards"); err != nil || len(cards) != 7 {
		t.Fatalf("kept cards = %d %v", len(cards), err)
	}

	// An empty keep list prunes nothing.
	if removed, err := store.PruneRegionEntities(ctx, "jp", nil); err != nil || len(removed) != 0 {
		t.Fatalf("empty keep removed %v %v", removed, err)
	}
	entities, err := storedEntities(ctx, db, "jp")
	if err != nil || strings.Join(entities, ",") != "cardepisodes,cards,musicvocals,resourceboxdetails,resourceboxes" {
		t.Fatalf("entities = %v %v", entities, err)
	}
}

func storedEntities(ctx context.Context, db *DB, region string) ([]string, error) {
	rows, err := db.Pool.Query(ctx, `SELECT entity FROM master_entities WHERE region = $1`, region)
	if err != nil {
		return nil, err
	}
	entities, err := pgx.CollectRows(rows, pgx.RowTo[string])
	slices.Sort(entities)
	return entities, err
}

func TestPostgresStoreReportsRegionData(t *testing.T) {
	store, _ := newPostgresStoreForTest(t, "")
	ctx := context.Background()
	if has, err := store.HasRegionData(ctx, "jp"); err != nil || has {
		t.Fatalf("empty store has data: %v %v", has, err)
	}
	if err := store.StoreRegion(ctx, "jp", map[string]any{"cards.json": []json.RawMessage{}}); err != nil {
		t.Fatalf("store empty entity: %v", err)
	}
	if has, _ := store.HasRegionData(ctx, "jp"); has {
		t.Fatal("region with only an empty entity has data")
	}
	if err := store.StoreRegion(ctx, "jp", map[string]any{"gachas.json": rawRecords(t, `{"id":1}`)}); err != nil {
		t.Fatalf("store: %v", err)
	}
	if has, err := store.HasRegionData(ctx, "jp"); err != nil || !has {
		t.Fatalf("populated region has no data: %v %v", has, err)
	}
	if has, _ := store.HasRegionData(ctx, "en"); has {
		t.Fatal("other region has data")
	}
	counts, err := store.RegionRecordCounts(ctx)
	if err != nil || len(counts) != 1 || counts["jp"] != 1 {
		t.Fatalf("region record counts = %v, %v; want jp=1", counts, err)
	}
}

func TestPostgresStoreRewritesOnlyWhatChanged(t *testing.T) {
	store, db := newPostgresStoreForTest(t, "")
	ctx := context.Background()
	payload := map[string]any{"cardEpisodes.json": rawRecords(t, `{"id":1,"cardId":1}`, `{"id":2,"cardId":1}`)}
	if err := store.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store: %v", err)
	}
	updatedAt := func() time.Time {
		var updated time.Time
		if err := db.Pool.QueryRow(ctx, `SELECT updated_at FROM master_entities WHERE region = 'jp' AND entity = 'cardepisodes'`).Scan(&updated); err != nil {
			t.Fatalf("read entity: %v", err)
		}
		return updated
	}
	var blockBefore []byte
	if err := db.Pool.QueryRow(ctx, `SELECT xmin::text::bytea FROM master_blocks WHERE region = 'jp' AND entity = 'cardepisodes'`).Scan(&blockBefore); err != nil {
		t.Fatalf("read block: %v", err)
	}
	firstUpdate := updatedAt()

	// The same records again: the blocks are left alone.
	if err := store.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store again: %v", err)
	}
	var blockAfter []byte
	if err := db.Pool.QueryRow(ctx, `SELECT xmin::text::bytea FROM master_blocks WHERE region = 'jp' AND entity = 'cardepisodes'`).Scan(&blockAfter); err != nil {
		t.Fatalf("read block: %v", err)
	}
	if string(blockBefore) != string(blockAfter) {
		t.Fatal("unchanged records rewrote their block")
	}
	if secondUpdate := updatedAt(); !secondUpdate.After(firstUpdate) {
		t.Fatal("entity row not touched by the second store")
	}
}

func TestBlockSortKeySQLFunctionMatchesGo(t *testing.T) {
	db := openMigratedTestDB(t)
	keys := []string{
		"0", "7", "10", "007", "-1", "1.5", "", "18446744073709551615", "99999999999999999999",
		"100000000000000000000", "auto:ab", "composite:v1:13:resourceboxes2:id1:5", "日本", "1a", "a1", " 1",
	}
	for _, key := range keys {
		var got string
		if err := db.Pool.QueryRow(context.Background(), `SELECT master_block_sort_key($1)`, key).Scan(&got); err != nil {
			t.Fatalf("sort key %q: %v", key, err)
		}
		if want := masterdata.BlockSortKey(key); got != want {
			t.Errorf("master_block_sort_key(%q) = %q, Go gives %q", key, got, want)
		}
	}
}

func TestBlockCodecRoundTrip(t *testing.T) {
	keys := []string{"2", "auto:x", "10", "2", "composite:v1:1:a"}
	records := [][]byte{[]byte(`{"id":2,"v":"old"}`), []byte(`{"n":1}`), []byte(`{"id":10}`), []byte(`{"id":2,"v":"new"}`), []byte(`{"a":"<&>"}`)}
	blocks, err := buildBlocks(blockEntries(keys, records))
	if err != nil || len(blocks) != 1 {
		t.Fatalf("blocks = %d %v", len(blocks), err)
	}
	entries, err := decodeBlock(blocks[0].body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var got []string
	for _, entry := range entries {
		got = append(got, fmt.Sprintf("%s%v%s", entry.key, entry.positions, entry.record))
	}
	want := `2[0 3]{"id":2,"v":"new"}|10[2]{"id":10}|auto:x[1]{"n":1}|composite:v1:1:a[4]{"a":"<&>"}`
	if strings.Join(got, "|") != want {
		t.Fatalf("entries = %s", strings.Join(got, "|"))
	}
	if blocks[0].firstKey != masterdata.BlockSortKey("2") {
		t.Fatalf("first key = %s", blocks[0].firstKey)
	}
}
