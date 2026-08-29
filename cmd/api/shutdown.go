package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// ErrShutdownTimeout is returned when background workers (sync, webhook, hub)
// do not stop within the graceful-shutdown deadline.
var ErrShutdownTimeout = errors.New("graceful shutdown timed out")

// syncWorkerWaiter is implemented by the master-data sync usecase so shutdown can
// wait for in-flight lifecycle-managed sync workers.
type syncWorkerWaiter interface {
	Wait()
}

// webhookShutdown is implemented by the GitHub webhook handler so shutdown can
// close the admission gate and wait for in-flight webhook-triggered syncs.
type webhookShutdown interface {
	RejectNewSubmissions()
	WaitForInflight(ctx context.Context) error
}

// eventHubCloser is implemented by the master-data event hub.
type eventHubCloser interface {
	Close()
}

// newShutdownSignalChannel returns a buffered channel subscribed to SIGTERM and
// SIGINT. The buffer allows a second signal to be observed without being dropped.
func newShutdownSignalChannel() chan os.Signal {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	return sigCh
}

// handleShutdownSignals blocks until the first SIGTERM/SIGINT. On receipt it
// starts the shutdown deadline timer (NOT at process start), publishes the
// deadline context (and its cancel func) over startedCh, then gracefully shuts
// down the HTTP server. If the deadline elapses it force-closes remaining
// connections and returns an error so the caller can surface a non-zero exit. A
// second signal (still buffered) forces an immediate close instead of being
// silently ignored; that operator-initiated force-close is not treated as a
// drain timeout error. The returned error is non-nil only when the graceful
// drain actually exceeds the first-signal deadline. The deadline context is
// owned by the caller after it is published, so the caller must cancel it once
// the worker drain is finished. drainDone is closed by the caller after the
// worker drain so the deferred cancel fallback never fires while the shared
// deadline is still required.
func handleShutdownSignals(sigCh <-chan os.Signal, server *http.Server, logger *zap.SugaredLogger, timeout time.Duration, startedCh chan<- context.Context, cancelCh chan<- context.CancelFunc, drainDone <-chan struct{}) error {
	sig, ok := <-sigCh
	if !ok {
		return nil
	}
	logger.Infow("received shutdown signal", "signal", sig.String())

	// The shutdown timer starts here, at signal receipt, so the grace period is
	// not consumed by normal runtime before a shutdown is requested. The deadline
	// context is passed separately (never stored in a struct, godre/S8242) and
	// ownership of its cancel func is transferred to the caller via cancelCh.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), timeout)

	// The cancel func is handed to the caller via cancelCh so the worker drain can
	// use the remaining grace period. A sync.Once-guarded fallback is deferred
	// immediately after creation (godre/S8188): if the caller never invokes the
	// handed-off cancel (e.g. main exits before the drain), the deadline timer is
	// still released. The caller's wrapper shares the same Once, so cancel runs
	// exactly once. The handler blocks on drainDone (closed by the caller after
	// the worker drain) before returning, so the deferred fallback never releases
	// the shared deadline while it is still needed.
	var shutdownCancelOnce sync.Once
	cancelShutdown := func() { shutdownCancelOnce.Do(shutdownCancel) }
	defer cancelShutdown()

	// Inform the main goroutine which deadline bounds the worker drain. The
	// caller now owns cancelShutdown and must invoke it after the worker drain
	// completes.
	startedCh <- shutdownCtx
	cancelCh <- cancelShutdown

	// A second signal (still buffered) forces an immediate close instead of being
	// silently swallowed. The watcher exits when the first-signal shutdown
	// completes (watcherCancel runs as the handler returns) or when it acts on a
	// second signal, so it never lingers to consume unrelated future signals.
	watcherCtx, watcherCancel := context.WithCancel(context.Background())
	defer watcherCancel()
	go func() {
		select {
		case sig2, ok := <-sigCh:
			if ok {
				logger.Infow("received second shutdown signal; forcing immediate close", "signal", sig2.String())
				_ = server.Close()
			}
		case <-watcherCtx.Done():
		}
	}()

	if err := server.Shutdown(shutdownCtx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			// The grace period elapsed before in-flight requests drained. Force-close
			// any remaining connections and report the drain timeout so the caller
			// can return a non-zero exit.
			logger.Errorw("http server graceful shutdown exceeded deadline; forcing close", "error", err)
			_ = server.Close()
			<-drainDone
			return fmt.Errorf("http graceful drain exceeded deadline: %w", err)
		}
		// Any other Shutdown error (e.g. forced close triggered by a second signal,
		// or an already-closed listener) is not a deadline timeout; treat it as an
		// expected force-close and do not fail the exit.
		logger.Warnw("http server forced close completed", "error", err)
		<-drainDone
		return nil
	}
	// The first-signal shutdown is complete, so the watcher no longer needs to
	// watch for a second signal. Release its context now (before blocking on the
	// caller's drain signal) so the watcher goroutine exits and the buffered
	// signal channel stays free for unrelated future signals.
	watcherCancel()
	// Block until the caller finishes the worker drain and releases the shared
	// deadline. The deferred cancelShutdown is a fallback that fires only if the
	// caller never invokes the handed-off cancel.
	<-drainDone
	return nil
}

// shutdownHTTPServer gracefully drains the HTTP server within ctx. It returns a
// non-nil error on timeout (and force-closes connections as a fallback).
func shutdownHTTPServer(ctx context.Context, server *http.Server, logger *zap.SugaredLogger) error {
	if err := server.Shutdown(ctx); err != nil {
		logger.Errorw("http server graceful shutdown failed", "error", err)
		_ = server.Close()
		return fmt.Errorf("http server shutdown: %w", err)
	}
	return nil
}

// waitForBackgroundWorkers stops accepting new webhook submissions, then waits
// for in-flight webhook-triggered syncs, the lifecycle-managed sync workers, and
// the event hub to stop, all bounded by ctx. It returns ErrShutdownTimeout if
// the deadline elapses so callers can surface a non-zero exit.
func waitForBackgroundWorkers(
	ctx context.Context,
	logger *zap.SugaredLogger,
	lifecycleWG *sync.WaitGroup,
	syncUsecase syncWorkerWaiter,
	webhook webhookShutdown,
	hub eventHubCloser,
) error {
	if webhook != nil {
		webhook.RejectNewSubmissions()
	}

	// 1) Startup lifecycle workers (migration/warmup/auto-sync/recovery).
	done := make(chan struct{})
	go func() {
		lifecycleWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("%w: startup workers did not finish: %v", ErrShutdownTimeout, ctx.Err())
	}

	// 2) In-flight webhook-triggered syncs (admission gate already closed).
	if webhook != nil {
		if err := webhook.WaitForInflight(ctx); err != nil {
			return fmt.Errorf("%w: %v", ErrShutdownTimeout, err)
		}
	}

	// 3) Lifecycle-managed sync workers (admin StartSync, webhook SyncRegion).
	if syncUsecase != nil {
		wgDone := make(chan struct{})
		go func() {
			syncUsecase.Wait()
			close(wgDone)
		}()
		select {
		case <-wgDone:
		case <-ctx.Done():
			return fmt.Errorf("%w: sync workers did not finish: %v", ErrShutdownTimeout, ctx.Err())
		}
	}

	// 4) Event hub is closed via the server shutdown callback; ensure it is
	//    closed (idempotent) so no SSE subscriber goroutines leak.
	if hub != nil {
		hub.Close()
	}

	return nil
}
