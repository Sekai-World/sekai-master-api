package masterdata

import (
	"errors"
	"time"
)

// Distributed sync coordination contract (docs/distributed-sync-coordination.md).
// The lease lives in PostgreSQL; the fencing token is issued atomically with
// each acquisition and is durable across Redis loss.

// ErrSyncLeaseHeld reports that another owner holds an unexpired sync lease.
var ErrSyncLeaseHeld = errors.New("master data sync lease is held by another owner")

// ErrSyncLeaseLost reports that a heartbeat renewal failed for a claim the
// holder believed it owned; the holder must stop writing immediately.
var ErrSyncLeaseLost = errors.New("master data sync lease was lost")

// ErrFencedOut reports that a status write was rejected because its fencing
// token is older than the lease's current token (a newer owner has taken over).
var ErrFencedOut = errors.New("master data sync status write fenced out by a newer lease owner")

// SyncLeaseClaim identifies one successful lease acquisition.
type SyncLeaseClaim struct {
	Holder string
	Token  int64
}

// SyncLeaseState is a point-in-time view of the sync lease, used for webhook
// admission probes and diagnostics.
type SyncLeaseState struct {
	Held      bool
	Holder    string
	Token     int64
	ExpiresAt time.Time
}
