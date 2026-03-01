package usecase

import (
	"context"
	"sync"

	"sekai-master-api/internal/domain/masterdata"
)

type MasterDataEventHub struct {
	mu          sync.RWMutex
	subscribers map[chan masterdata.SyncUpdatedEvent]struct{}
}

func NewMasterDataEventHub() *MasterDataEventHub {
	return &MasterDataEventHub{
		subscribers: make(map[chan masterdata.SyncUpdatedEvent]struct{}),
	}
}

func (hub *MasterDataEventHub) PublishMasterDataUpdated(_ context.Context, event masterdata.SyncUpdatedEvent) error {
	hub.mu.RLock()
	defer hub.mu.RUnlock()

	for subscriber := range hub.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}

	return nil
}

func (hub *MasterDataEventHub) Subscribe() (<-chan masterdata.SyncUpdatedEvent, func()) {
	channel := make(chan masterdata.SyncUpdatedEvent, 8)

	hub.mu.Lock()
	hub.subscribers[channel] = struct{}{}
	hub.mu.Unlock()

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
