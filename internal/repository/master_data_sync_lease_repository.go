package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"sekai-master-api/internal/domain/masterdata"
)

// MasterDataSyncLeaseRepository owns the master_data_sync_leases table. All
// acquisition/renewal decisions are single-statement compare-and-swaps so two
// racers can never both observe success; the fencing token is bumped in the
// same statement that transfers ownership. Timestamps are computed by the
// caller and passed as parameters so expiry comparisons never mix database
// and application clocks.
type MasterDataSyncLeaseRepository struct {
	db     *sql.DB
	driver string
}

func NewMasterDataSyncLeaseRepository(db *sql.DB, driver string) *MasterDataSyncLeaseRepository {
	return &MasterDataSyncLeaseRepository{
		db:     db,
		driver: strings.ToLower(strings.TrimSpace(driver)),
	}
}

// MasterDataSyncLeaseRecord is one row of master_data_sync_leases.
type MasterDataSyncLeaseRecord struct {
	Name            string
	Holder          string
	FencingToken    int64
	AcquiredAt      time.Time
	ExpiresAt       time.Time
	LastHeartbeatAt time.Time
}

// Acquire inserts the lease or takes over an expired one, atomically issuing
// the next fencing token. It reports acquired=false (no error) when another
// unexpired owner holds the lease.
func (repository *MasterDataSyncLeaseRepository) Acquire(ctx context.Context, name string, holder string, ttl time.Duration) (*MasterDataSyncLeaseRecord, bool, error) {
	if repository.db == nil {
		return nil, false, fmt.Errorf("lease repository has no database")
	}

	now := time.Now().UTC()
	expiresAt := now.Add(ttl)

	query := `
INSERT INTO master_data_sync_leases (
	name, holder, fencing_token, acquired_at, expires_at, last_heartbeat_at
) VALUES (?, ?, 1, ?, ?, ?)
ON CONFLICT (name) DO UPDATE SET
	holder = EXCLUDED.holder,
	fencing_token = master_data_sync_leases.fencing_token + 1,
	acquired_at = EXCLUDED.acquired_at,
	expires_at = EXCLUDED.expires_at,
	last_heartbeat_at = EXCLUDED.last_heartbeat_at
WHERE master_data_sync_leases.expires_at <= EXCLUDED.last_heartbeat_at
RETURNING fencing_token, acquired_at, expires_at, last_heartbeat_at`
	if repository.isPostgres() {
		query = `
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
RETURNING fencing_token, acquired_at, expires_at, last_heartbeat_at`
	}

	record := &MasterDataSyncLeaseRecord{Name: name, Holder: holder}
	err := repository.db.QueryRowContext(ctx, query, name, holder, now, expiresAt, now).Scan(
		&record.FencingToken,
		&record.AcquiredAt,
		&record.ExpiresAt,
		&record.LastHeartbeatAt,
	)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("acquire sync lease: %w", err)
	}
	return record, true, nil
}

// Renew extends the lease only while the holder is unchanged. Renewal of a
// briefly expired-but-untaken lease still succeeds: the token did not move,
// so no fencing boundary was crossed.
func (repository *MasterDataSyncLeaseRepository) Renew(ctx context.Context, name string, holder string, ttl time.Duration) (bool, error) {
	if repository.db == nil {
		return false, fmt.Errorf("lease repository has no database")
	}

	now := time.Now().UTC()
	expiresAt := now.Add(ttl)

	var result sql.Result
	var err error
	if repository.isPostgres() {
		result, err = repository.db.ExecContext(
			ctx,
			`UPDATE master_data_sync_leases SET expires_at = $3, last_heartbeat_at = $4 WHERE name = $1 AND holder = $2`,
			name, holder, expiresAt, now,
		)
	} else {
		result, err = repository.db.ExecContext(
			ctx,
			`UPDATE master_data_sync_leases SET expires_at = ?, last_heartbeat_at = ? WHERE name = ? AND holder = ?`,
			expiresAt, now, name, holder,
		)
	}
	if err != nil {
		return false, fmt.Errorf("renew sync lease: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("renew sync lease rows: %w", err)
	}
	return affected > 0, nil
}

// Release expires the lease in place while the caller is still the holder.
// The row (and therefore the fencing token) is deliberately kept so tokens
// stay monotonic across release/reacquire cycles; a failing release is safe
// because the lease simply expires on its own.
func (repository *MasterDataSyncLeaseRepository) Release(ctx context.Context, name string, holder string) error {
	if repository.db == nil {
		return nil
	}

	now := time.Now().UTC()
	query := `UPDATE master_data_sync_leases SET expires_at = ?, last_heartbeat_at = ? WHERE name = ? AND holder = ?`
	args := []any{now, now, name, holder}
	if repository.isPostgres() {
		query = `UPDATE master_data_sync_leases SET expires_at = $3, last_heartbeat_at = $4 WHERE name = $1 AND holder = $2`
		args = []any{name, holder, now, now}
	}

	if _, err := repository.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("release sync lease: %w", err)
	}
	return nil
}

// Get returns the current lease row, if any.
func (repository *MasterDataSyncLeaseRepository) Get(ctx context.Context, name string) (*MasterDataSyncLeaseRecord, bool, error) {
	if repository.db == nil {
		return nil, false, nil
	}

	query := `SELECT holder, fencing_token, acquired_at, expires_at, last_heartbeat_at FROM master_data_sync_leases WHERE name = ?`
	if repository.isPostgres() {
		query = `SELECT holder, fencing_token, acquired_at, expires_at, last_heartbeat_at FROM master_data_sync_leases WHERE name = $1`
	}

	record := &MasterDataSyncLeaseRecord{Name: name}
	err := repository.db.QueryRowContext(ctx, query, name).Scan(
		&record.Holder,
		&record.FencingToken,
		&record.AcquiredAt,
		&record.ExpiresAt,
		&record.LastHeartbeatAt,
	)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get sync lease: %w", err)
	}
	return record, true, nil
}

func (repository *MasterDataSyncLeaseRepository) isPostgres() bool {
	return repository.driver == "pgx" || repository.driver == "postgres" || repository.driver == "postgresql"
}

// MasterDataSyncLeaseCoordinator binds the lease repository to one lease
// name, holder identity, and TTL, and implements the usecase-side
// MasterDataSyncLeaseCoordinator contract.
type MasterDataSyncLeaseCoordinator struct {
	repository *MasterDataSyncLeaseRepository
	name       string
	holder     string
	ttl        time.Duration
}

func NewMasterDataSyncLeaseCoordinator(repository *MasterDataSyncLeaseRepository, name string, holder string, ttl time.Duration) *MasterDataSyncLeaseCoordinator {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &MasterDataSyncLeaseCoordinator{
		repository: repository,
		name:       name,
		holder:     holder,
		ttl:        ttl,
	}
}

func (coordinator *MasterDataSyncLeaseCoordinator) Acquire(ctx context.Context) (masterdata.SyncLeaseClaim, error) {
	record, acquired, err := coordinator.repository.Acquire(ctx, coordinator.name, coordinator.holder, coordinator.ttl)
	if err != nil {
		return masterdata.SyncLeaseClaim{}, err
	}
	if !acquired {
		return masterdata.SyncLeaseClaim{}, masterdata.ErrSyncLeaseHeld
	}
	return masterdata.SyncLeaseClaim{Holder: record.Holder, Token: record.FencingToken}, nil
}

func (coordinator *MasterDataSyncLeaseCoordinator) Renew(ctx context.Context, claim masterdata.SyncLeaseClaim) error {
	renewed, err := coordinator.repository.Renew(ctx, coordinator.name, claim.Holder, coordinator.ttl)
	if err != nil {
		return err
	}
	if !renewed {
		return masterdata.ErrSyncLeaseLost
	}
	return nil
}

func (coordinator *MasterDataSyncLeaseCoordinator) Release(ctx context.Context, claim masterdata.SyncLeaseClaim) error {
	return coordinator.repository.Release(ctx, coordinator.name, claim.Holder)
}

func (coordinator *MasterDataSyncLeaseCoordinator) State(ctx context.Context) (masterdata.SyncLeaseState, error) {
	record, found, err := coordinator.repository.Get(ctx, coordinator.name)
	if err != nil || !found {
		return masterdata.SyncLeaseState{}, err
	}
	return masterdata.SyncLeaseState{
		Held:      record.ExpiresAt.After(time.Now().UTC()),
		Holder:    record.Holder,
		Token:     record.FencingToken,
		ExpiresAt: record.ExpiresAt,
	}, nil
}
