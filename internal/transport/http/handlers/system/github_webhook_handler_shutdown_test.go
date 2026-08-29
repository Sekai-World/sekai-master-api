package system

import (
	"context"
	"testing"
	"time"
)

// blockingSyncer blocks SyncRegion until the context is cancelled, simulating an
// in-flight webhook-triggered sync that must stop on graceful shutdown.
type blockingSyncer struct{}

func (blockingSyncer) SyncRegion(ctx context.Context, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestRejectNewSubmissionsAndWaitForInflight(t *testing.T) {
	handler := NewGitHubWebhookHandler(nil, blockingSyncer{}, 0, "")

	// Simulate an in-flight webhook sync goroutine that is tracked before spawn.
	handler.inflight.Add(1)
	go func() {
		defer handler.inflight.Done()
		time.Sleep(30 * time.Millisecond)
	}()

	handler.RejectNewSubmissions()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := handler.WaitForInflight(ctx); err != nil {
		t.Fatalf("expected in-flight wait to complete, got %v", err)
	}
}

func TestWaitForInflightTimesOut(t *testing.T) {
	handler := NewGitHubWebhookHandler(nil, blockingSyncer{}, 0, "")

	handler.inflight.Add(1)
	go func() {
		defer handler.inflight.Done()
		time.Sleep(500 * time.Millisecond)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := handler.WaitForInflight(ctx); err == nil {
		t.Fatal("expected timeout error from WaitForInflight")
	}
}

func TestTriggerRegionSyncStopsOnLifecycleCancel(t *testing.T) {
	lifecycleCtx, cancel := context.WithCancel(context.Background())
	handler := NewGitHubWebhookHandler(nil, blockingSyncer{}, 0, "")

	done := make(chan struct{})
	go func() {
		handler.triggerRegionSync(context.Background(), lifecycleCtx, "jp", "Sekai-World", "sekai-master-data-jp", "main")
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	// Graceful shutdown cancels the lifecycle context; the sync must stop.
	cancel()

	select {
	case <-done:
		// sync goroutine returned after lifecycle cancellation.
	case <-time.After(2 * time.Second):
		t.Fatal("triggerRegionSync did not stop after lifecycle cancellation")
	}
}
