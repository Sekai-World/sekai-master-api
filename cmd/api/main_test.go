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
	handlerDone := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		close(handlerDone)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()

	sigCh := make(chan os.Signal, 1)
	startedCh := make(chan context.Context, 1)
	cancelCh := make(chan context.CancelFunc, 1)
	go handleShutdownSignals(sigCh, server, nopLogger(), 2*time.Second, startedCh, cancelCh)

	// Start a request that will be drained within the deadline.
	clientDone := make(chan int, 1)
	go func() {
		resp, reqErr := http.Get("http://" + listener.Addr().String())
		if reqErr != nil {
			clientDone <- 0
			return
		}
		defer resp.Body.Close()
		clientDone <- resp.StatusCode
	}()

	time.Sleep(10 * time.Millisecond)
	sigCh <- syscall.SIGTERM

	select {
	case status := <-clientDone:
		if status != http.StatusOK {
			t.Fatalf("expected drained request to return 200, got %d", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request was not drained before timeout")
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("expected http.ErrServerClosed, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after shutdown signal")
	}
}

func TestHandleShutdownSignalsTimeoutForceCloses(t *testing.T) {
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Hold the connection open past the deadline.
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()

	sigCh := make(chan os.Signal, 1)
	startedCh := make(chan context.Context, 1)
	cancelCh := make(chan context.CancelFunc, 1)
	go handleShutdownSignals(sigCh, server, nopLogger(), 40*time.Millisecond, startedCh, cancelCh)

	// Fire a request that outlives the shutdown deadline.
	go func() {
		resp, reqErr := http.Get("http://" + listener.Addr().String())
		if reqErr == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()

	time.Sleep(10 * time.Millisecond)
	start := time.Now()
	sigCh <- syscall.SIGTERM

	select {
	case err := <-errCh:
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
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Hold the connection open past the deadline.
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()

	sigCh := make(chan os.Signal, 1)
	startedCh := make(chan context.Context, 1)
	cancelCh := make(chan context.CancelFunc, 1)
	handlerErrCh := make(chan error, 1)
	go func() {
		handlerErrCh <- handleShutdownSignals(sigCh, server, nopLogger(), 40*time.Millisecond, startedCh, cancelCh)
	}()

	go func() {
		resp, reqErr := http.Get("http://" + listener.Addr().String())
		if reqErr == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()

	time.Sleep(10 * time.Millisecond)
	sigCh <- syscall.SIGTERM

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
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after drain timeout force-close")
	}
}

func TestHandleShutdownSignalsSecondSignalDoesNotSurfaceDrainError(t *testing.T) {
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1 * time.Second)
		w.WriteHeader(http.StatusOK)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()

	sigCh := make(chan os.Signal, 1)
	startedCh := make(chan context.Context, 1)
	cancelCh := make(chan context.CancelFunc, 1)
	handlerErrCh := make(chan error, 1)
	go func() {
		handlerErrCh <- handleShutdownSignals(sigCh, server, nopLogger(), 30*time.Second, startedCh, cancelCh)
	}()

	time.Sleep(10 * time.Millisecond)
	sigCh <- syscall.SIGTERM
	sigCh <- syscall.SIGTERM

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
	case <-errCh:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop after second signal force-close")
	}
}

func TestHandleShutdownSignalsSecondSignalForceCloses(t *testing.T) {
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(1 * time.Second)
		w.WriteHeader(http.StatusOK)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()

	sigCh := make(chan os.Signal, 1)
	startedCh := make(chan context.Context, 1)
	cancelCh := make(chan context.CancelFunc, 1)
	go handleShutdownSignals(sigCh, server, nopLogger(), 30*time.Second, startedCh, cancelCh)

	time.Sleep(10 * time.Millisecond)
	// First signal starts a long graceful drain; second signal must force-close now.
	sigCh <- syscall.SIGTERM
	start := time.Now()
	sigCh <- syscall.SIGTERM

	select {
	case err := <-errCh:
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

// --- fakes for waitForBackgroundWorkers ---

type fakeWorkerWaiter struct{ wg *sync.WaitGroup }

func (f fakeWorkerWaiter) Wait() { f.wg.Wait() }

type fakeHub struct{ closed bool }

func (f *fakeHub) Close() { f.closed = true }

// TestHandleShutdownSignalsContextStaysValidAfterReturn verifies that the
// deadline context handed to the caller is NOT canceled when the handler returns
// after a normal first-signal shutdown. The caller owns its cancellation, so the
// context must remain usable (still within its grace period) for the worker
// drain; otherwise workers would be falsely reported as timed out.
func TestHandleShutdownSignalsContextStaysValidAfterReturn(t *testing.T) {
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()

	sigCh := make(chan os.Signal, 1)
	startedCh := make(chan context.Context, 1)
	cancelCh := make(chan context.CancelFunc, 1)
	go func() { _ = handleShutdownSignals(sigCh, server, nopLogger(), 5*time.Second, startedCh, cancelCh) }()

	time.Sleep(10 * time.Millisecond)
	sigCh <- syscall.SIGTERM

	var ctrlCtx context.Context
	var ctrlCancel context.CancelFunc
	select {
	case ctrlCtx = <-startedCh:
		ctrlCancel = <-cancelCh
		// Immediately after the handler publishes the context, it must be valid
		// (timer started at signal receipt; it is far from elapsing).
		if ctrlCtx.Err() != nil {
			t.Fatalf("shutdown context already invalid when published: %v", ctrlCtx.Err())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not publish shutdown control")
	}

	// Wait for the server to fully stop so the handler has returned.
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after shutdown signal")
	}

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
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()

	sigCh := make(chan os.Signal, 1)
	startedCh := make(chan context.Context, 1)
	cancelCh := make(chan context.CancelFunc, 1)
	go func() { _ = handleShutdownSignals(sigCh, server, nopLogger(), 5*time.Second, startedCh, cancelCh) }()

	time.Sleep(10 * time.Millisecond)
	// First signal triggers a normal, fast shutdown.
	sigCh <- syscall.SIGTERM

	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after shutdown signal")
	}

	// Give the handler and its watcher goroutine time to finish and exit.
	time.Sleep(100 * time.Millisecond)

	// A future unrelated signal must NOT be consumed by the exited watcher; it
	// should stay in the buffered channel for the caller.
	sigCh <- syscall.SIGTERM
	select {
	case s := <-sigCh:
		if s != syscall.SIGTERM {
			t.Fatalf("unexpected signal value: %v", s)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("watcher still blocked on / consuming signals after shutdown completed")
	}
}
