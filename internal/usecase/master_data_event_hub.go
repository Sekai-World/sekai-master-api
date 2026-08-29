package usecase

import (
	"context"
	"sync"

	"sekai-master-api/internal/domain/masterdata"
)

type MasterDataEventHub struct {
	mu          sync.RWMutex
	subscribers map[chan masterdata.SyncUpdatedEvent]struct{}
	closed      bool
	closeOnce   sync.Once
}

func NewMasterDataEventHub() *MasterDataEventHub {
	return &MasterDataEventHub{
		subscribers: make(map[chan masterdata.SyncUpdatedEvent]struct{}),
	}
}

// PublishMasterDataUpdated fans out an event to all current subscribers. It is a
// no-op once the hub is closed, and never panics on a closed subscriber channel
// because Close acquires the write lock while Publish holds the read lock.
func (hub *MasterDataEventHub) PublishMasterDataUpdated(_ context.Context, event masterdata.SyncUpdatedEvent) error {
	if hub == nil {
		return nil
	}

	hub.mu.RLock()
	defer hub.mu.RUnlock()

	if hub.closed {
		return nil
	}

	for subscriber := range hub.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}

	return nil
}

// Subscribe registers a new subscriber. If the hub has already been closed, it
// returns an already-closed channel so the consumer ends immediately.
func (hub *MasterDataEventHub) Subscribe() (<-chan masterdata.SyncUpdatedEvent, func()) {
	if hub == nil {
		closedCh := make(chan masterdata.SyncUpdatedEvent)
		close(closedCh)
		return closedCh, func() {}
	}

	hub.mu.Lock()
	defer hub.mu.Unlock()

	if hub.closed {
		closedCh := make(chan masterdata.SyncUpdatedEvent)
		close(closedCh)
		return closedCh, func() {}
	}

	channel := make(chan masterdata.SyncUpdatedEvent, 8)
	hub.subscribers[channel] = struct{}{}

	unsubscribe := func() {
		hub.mu.Lock()
		if _, ok := hub.subscribers[channel]; ok {
			delete(hub.subscribers, channel)
			close(channel)
		}
		hub.mu.Unlock()
	}

	return channel, unsubscribe
}

// Close shuts down the hub idempotently, closing every subscriber channel so
// server-side SSE streams terminate during graceful shutdown instead of holding
// long-lived connections open past the grace period. It is safe to call multiple
// times and safe to call concurrently with Publish/Subscribe.
func (hub *MasterDataEventHub) Close() {
	if hub == nil {
		return
	}
	hub.closeOnce.Do(func() {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		hub.closed = true
		for subscriber := range hub.subscribers {
			delete(hub.subscribers, subscriber)
			close(subscriber)
		}
	})
}
