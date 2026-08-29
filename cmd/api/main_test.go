package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"go.uber.org/zap"
)

func nopLogger() *zap.SugaredLogger { return zap.NewNop().Sugar() }

// shutdownSignalHarness wires an HTTP server, its listener, and the signal
// channels used by handleShutdownSignals so the repeated scaffolding shared by
// the shutdown-signal tests lives in one place.
type shutdownSignalHarness struct {
	server    *http.Server
	listener  net.Listener
	errCh     chan error
	sigCh     chan os.Signal
	startedCh chan context.Context
	cancelCh  chan context.CancelFunc
}

func newShutdownSignalHarness(t *testing.T, handler http.HandlerFunc) *shutdownSignalHarness {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	h := &shutdownSignalHarness{
		server:    &http.Server{Handler: handler},
		listener:  listener,
		errCh:     make(chan error, 1),
		sigCh:     make(chan os.Signal, 1),
		startedCh: make(chan context.Context, 1),
		cancelCh:  make(chan context.CancelFunc, 1),
	}
	go func() { h.errCh <- h.server.Serve(listener) }()
	return h
}

// start launches the shutdown-signal handler with the given graceful deadline.
func (h *shutdownSignalHarness) start(connectTimeout time.Duration) {
	go handleShutdownSignals(h.sigCh, h.server, nopLogger(), connectTimeout, h.startedCh, h.cancelCh)
}

func (h *shutdownSignalHarness) waitServerStopped(t *testing.T) {
	t.Helper()
	select {
	case err := <-h.errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("expected http.ErrServerClosed, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after shutdown signal")
	}
}

func (h *shutdownSignalHarness) controlContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	select {
	case ctx := <-h.startedCh:
		cancel := <-h.cancelCh
		if ctx.Err() != nil {
			t.Fatalf("shutdown context already invalid when published: %v", ctx.Err())
		}
		return ctx, cancel
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not publish shutdown control")
	}
	return nil, nil
}

func TestShutdownHTTPServerGraceful(t *testing.T) {
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()

	// Let the server start accepting.
	time.Sleep(20 * time.Millisecond)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	shutdownHTTPServer(shutdownCtx, server, nopLogger())

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("expected http.ErrServerClosed, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after graceful shutdown")
	}
}

func TestWaitForBackgroundWorkersCompletes(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		wg.Done()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := waitForBackgroundWorkers(ctx, nopLogger(), wg, nil, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 1*time.Second {
		t.Fatalf("waited too long for workers to finish: %v", elapsed)
	}
}

func TestWaitForBackgroundWorkersTimeout(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(1) // never completed

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := waitForBackgroundWorkers(ctx, nopLogger(), wg, nil, nil, nil)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("expected ErrShutdownTimeout, got %v", err)
	}
	if elapsed < 40*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("expected bounded timeout near 50ms, got %v", elapsed)
	}
}

func TestWaitForBackgroundWorkersTimesOutOnSyncWorker(t *testing.T) {
	wg := &sync.WaitGroup{}
	wg.Add(1) // startup lifecycle worker never completes

	blocking := &fakeWorkerWaiter{wg: &sync.WaitGroup{}}
	blocking.wg.Add(1) // sync worker never completes

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	err := waitForBackgroundWorkers(ctx, nopLogger(), wg, blocking, nil, &fakeHub{})
	if !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("expected ErrShutdownTimeout from sync worker, got %v", err)
	}
}

func TestHandleShutdownSignalsFirstSignalShutsDownServer(t *testing.T) {
	h := newShutdownSignalHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	h.start(2 * time.Second)

	// Start a request that will be drained within the deadline.
	clientDone := make(chan int, 1)
	go func() {
		resp, reqErr := http.Get("http://" + h.listener.Addr().String())
		if reqErr != nil {
			clientDone <- 0
			return
		}
		defer resp.Body.Close()
		clientDone <- resp.StatusCode
	}()

	time.Sleep(10 * time.Millisecond)
	h.sigCh <- syscall.SIGTERM

	select {
	case status := <-clientDone:
		if status != http.StatusOK {
			t.Fatalf("expected drained request to return 200, got %d", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request was not drained before timeout")
	}

	h.waitServerStopped(t)
}

func TestHandleShutdownSignalsTimeoutForceCloses(t *testing.T) {
	h := newShutdownSignalHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		// Hold the connection open past the deadline.
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	h.start(40 * time.Millisecond)

	// Fire a request that outlives the shutdown deadline.
	go func() {
		resp, reqErr := http.Get("http://" + h.listener.Addr().String())
		if reqErr == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()

	time.Sleep(10 * time.Millisecond)
	start := time.Now()
	h.sigCh <- syscall.SIGTERM

	select {
	case err := <-h.errCh:
		// http.ErrServerClosed is returned for both graceful Shutdown and forced
		// Close; what matters is that the server stopped within the deadline rather
		// than holding the open connection for the full 500ms.
		_ = err
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Fatalf("server did not stop within shutdown deadline, elapsed=%v", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after shutdown timeout")
	}
}

func TestHandleShutdownSignalsReturnsErrorOnDrainTimeout(t *testing.T) {
	h := newShutdownSignalHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		// Hold the connection open past the deadline.
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	handlerErrCh := make(chan error, 1)
	go func() {
		handlerErrCh <- handleShutdownSignals(h.sigCh, h.server, nopLogger(), 40*time.Millisecond, h.startedCh, h.cancelCh)
	}()

	go func() {
		resp, reqErr := http.Get("http://" + h.listener.Addr().String())
		if reqErr == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()

	time.Sleep(10 * time.Millisecond)
	h.sigCh <- syscall.SIGTERM

	select {
	case serr := <-handlerErrCh:
		if !errors.Is(serr, context.DeadlineExceeded) {
			t.Fatalf("expected drain timeout error wrapping context.DeadlineExceeded, got %v", serr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handleShutdownSignals did not return after drain timeout")
	}

	// The server must have been force-closed so the process does not hang.
	select {
	case <-h.errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after drain timeout force-close")
	}
}

func TestHandleShutdownSignalsSecondSignalDoesNotSurfaceDrainError(t *testing.T) {
	h := newShutdownSignalHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1 * time.Second)
		w.WriteHeader(http.StatusOK)
	})

	handlerErrCh := make(chan error, 1)
	go func() {
		handlerErrCh <- handleShutdownSignals(h.sigCh, h.server, nopLogger(), 30*time.Second, h.startedCh, h.cancelCh)
	}()

	time.Sleep(10 * time.Millisecond)
	h.sigCh <- syscall.SIGTERM
	h.sigCh <- syscall.SIGTERM

	select {
	case serr := <-handlerErrCh:
		// A second-signal force-close is operator-initiated and must not be
		// reported as a drain timeout error.
		if serr != nil {
			t.Fatalf("expected nil error for second-signal force-close, got %v", serr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("handleShutdownSignals did not return after second signal")
	}

	select {
	case <-h.errCh:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop after second signal force-close")
	}
}

func TestHandleShutdownSignalsSecondSignalForceCloses(t *testing.T) {
	h := newShutdownSignalHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1 * time.Second)
		w.WriteHeader(http.StatusOK)
	})
	h.start(30 * time.Second)

	time.Sleep(10 * time.Millisecond)
	// First signal starts a long graceful drain; second signal must force-close now.
	h.sigCh <- syscall.SIGTERM
	start := time.Now()
	h.sigCh <- syscall.SIGTERM

	select {
	case err := <-h.errCh:
		_ = err
		// The 30s deadline would otherwise keep the connection open; the second
		// signal must force-close immediately.
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("second signal did not force-close promptly, elapsed=%v", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second signal did not force-close the server")
	}
}

// TestHandleShutdownSignalsContextStaysValidAfterReturn verifies that the
// deadline context handed to the caller is NOT canceled when the handler returns
// after a normal first-signal shutdown. The caller owns its cancellation, so the
// context must remain usable (still within its grace period) for the worker
// drain; otherwise workers would be falsely reported as timed out.
func TestHandleShutdownSignalsContextStaysValidAfterReturn(t *testing.T) {
	h := newShutdownSignalHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h.start(5 * time.Second)

	time.Sleep(10 * time.Millisecond)
	h.sigCh <- syscall.SIGTERM

	ctrlCtx, ctrlCancel := h.controlContext(t)

	// Wait for the server to fully stop so the handler has returned.
	h.waitServerStopped(t)

	// The handler returns right after server.Shutdown completes. Because the
	// handler no longer cancels the deadline, the caller's context must still be
	// valid here; if it were canceled, the worker drain (which derives from it)
	// would falsely time out.
	if ctrlCtx.Err() != nil {
		t.Fatalf("shutdown context canceled when handler returned; caller cannot use it for worker drain: %v", ctrlCtx.Err())
	}

	// Caller releases the timer.
	ctrlCancel()
	if ctrlCtx.Err() == nil {
		t.Fatal("expected shutdown context to be canceled after caller invokes cancel")
	}
}

// TestHandleShutdownSignalsWatcherDoesNotConsumeFutureSignal verifies that the
// second-signal watcher goroutine exits once the first-signal shutdown completes,
// so it does not linger to consume unrelated future signals. After a normal
// shutdown, a subsequent signal placed on the channel must remain available to
// the caller rather than being swallowed by the (now-exited) watcher.
func TestHandleShutdownSignalsWatcherDoesNotConsumeFutureSignal(t *testing.T) {
	h := newShutdownSignalHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h.start(5 * time.Second)

	time.Sleep(10 * time.Millisecond)
	// First signal triggers a normal, fast shutdown.
	h.sigCh <- syscall.SIGTERM

	h.waitServerStopped(t)

	// Give the handler and its watcher goroutine time to finish and exit.
	time.Sleep(100 * time.Millisecond)

	// A future unrelated signal must NOT be consumed by the exited watcher; it
	// should stay in the buffered channel for the caller.
	h.sigCh <- syscall.SIGTERM
	select {
	case s := <-h.sigCh:
		if s != syscall.SIGTERM {
			t.Fatalf("unexpected signal value: %v", s)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("watcher still blocked on / consuming signals after shutdown completed")
	}
}

// --- fakes for waitForBackgroundWorkers ---

type fakeWorkerWaiter struct{ wg *sync.WaitGroup }

func (f fakeWorkerWaiter) Wait() { f.wg.Wait() }

type fakeHub struct{ closed bool }

func (f *fakeHub) Close() { f.closed = true }
