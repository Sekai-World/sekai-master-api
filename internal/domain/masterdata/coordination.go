package masterdata

import (
	"context"
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

// ErrFencedOut reports that a status or data write was rejected because its
// fencing token is no longer the lease's current token (a newer owner has
// taken over).
var ErrFencedOut = errors.New("master data sync write fenced out by a newer lease owner")

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

type fencingTokenContextKey struct{}

// WithFencingToken marks ctx as running under the sync lease claim that
// issued token. Stores that fence data writes read it back with
// FencingTokenFromContext.
func WithFencingToken(ctx context.Context, token int64) context.Context {
	if token <= 0 {
		return ctx
	}
	return context.WithValue(ctx, fencingTokenContextKey{}, token)
}

// FencingTokenFromContext returns the sync lease token ctx runs under, or 0
// when the write is not made under a lease.
func FencingTokenFromContext(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	token, _ := ctx.Value(fencingTokenContextKey{}).(int64)
	return token
}
