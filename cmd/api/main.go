package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"go.uber.org/zap"

	"sekai-master-api/internal/auth"
	"sekai-master-api/internal/config"
	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/logging"
	"sekai-master-api/internal/observability"
	"sekai-master-api/internal/repository"
	"sekai-master-api/internal/startup"
	"sekai-master-api/internal/storage"
	transport "sekai-master-api/internal/transport/http"
	"sekai-master-api/internal/transport/http/swaggerdocs"
	"sekai-master-api/internal/usecase"
	"sekai-master-api/internal/version"
)

// @title sekai-master-api
// @version 1.0
// @description API for master data sync and card querying.
// @BasePath /api/v1
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization

func main() {
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err := runMigrationCommand(os.Args[2:]); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	applyRoleSubcommandFromArgs()

	cfg := config.Load()

	// Release builds override the generated swagger docs version (the
	// `@version 1.0` annotation stays the generation-time default).
	if version.IsRelease() {
		swaggerdocs.SwaggerInfo.Version = version.Version
	}

	cleanupLogger, err := logging.Setup(cfg.LogLevel, cfg.IsDevelopment(), cfg.LokiPushURL)
	if err != nil {
		panic(err)
	}
	defer cleanupLogger()
	logging.ConfigureGinWriters()

	logger := zap.S()

	cleanupObservability, err := observability.Setup(context.Background(), cfg)
	if err != nil {
		logger.Fatalf("failed to initialize observability: %v", err)
	}
	if err := observability.RegisterRuntimeMetrics(); err != nil {
		logger.Fatalf("failed to register runtime metrics: %v", err)
	}

	db, err := storage.OpenDB(cfg)
	if err != nil {
		logger.Fatalf("failed to initialize database: %v", err)
	}

	var tokenVerifier auth.TokenVerifier
	if cfg.NeedsAdminSurface() {
		tokenVerifier, err = auth.NewOIDCVerifier(context.Background(), cfg)
		if err != nil {
			logger.Fatalf("failed to initialize oidc verifier: %v", err)
		}
	}

	masterDataSources := buildMasterDataSources(cfg)
	masterDataStatusRepository := repository.NewMasterDataSyncStatusRepository(db, cfg.DatabaseDriver())
	masterDataLoader := repository.NewGitHubMasterDataRepository(
		time.Duration(cfg.MasterDataHTTPTimeout)*time.Second,
		cfg.MasterDataGitHubToken,
		cfg.MasterDataFileConcurrency,
		cfg.MasterDataHTTPRetryCount,
		time.Duration(cfg.MasterDataHTTPRetryBackoffMS)*time.Millisecond,
		cfg.MasterDataResumeBaseDir,
	)
	masterDataEventHub := usecase.NewMasterDataEventHub()
	masterDataCache, err := storage.NewRedisMasterDataCache(cfg)
	if err != nil {
		logger.Fatalf("failed to initialize redis master data cache: %v", err)
	}

	masterDataCacheCloser := masterDataCache.Close

	masterDataSyncUsecase := usecase.NewMasterDataSyncUsecase(
		masterDataSources,
		masterDataLoader,
		masterDataCache,
		masterDataStatusRepository,
		masterDataEventHub,
		cfg.MasterDataSyncConcurrency,
	)
	if cfg.MasterDataSyncTimeout > 0 {
		masterDataSyncUsecase.SetRegionTimeout(time.Duration(cfg.MasterDataSyncTimeout) * time.Second)
	}
	masterDataSyncUsecase.SetJobTimeout(time.Duration(cfg.MasterDataSyncJobTimeout) * time.Second)
	masterDataSyncUsecase.EnableDevelopmentBackupBootstrap(cfg.IsDevelopment())
	// Cancellable app lifecycle context; terminating it interrupts background
	// sync workers (admin StartSync, webhook SyncRegion) during graceful shutdown.
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()
	masterDataSyncUsecase.SetLifecycleContext(appCtx)
	if err := observability.RegisterMasterDataMetrics(masterDataSyncUsecase, masterDataCache); err != nil {
		logger.Fatalf("failed to register master data metrics: %v", err)
	}
	startupState := startup.NewState()
	router, gitHubWebhookHandler, err := transport.NewRouter(cfg, db, tokenVerifier, masterDataSyncUsecase, masterDataEventHub, startupState, appCtx)
	if err != nil {
		logger.Fatalf("failed to initialize router: %v", err)
	}

	listener, err := net.Listen("tcp", ":"+cfg.Port)
	if err != nil {
		logger.Fatalf("failed to listen on port %s: %v", cfg.Port, err)
	}
	logger.Infow("api server listening", "addr", listener.Addr().String(), "role", string(cfg.Role))

	server := &http.Server{Handler: router}
	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.Serve(listener)
	}()

	// The `serve` role is a pure public read workload: it must not run
	// migrations, search-index warmup, master-data sync, or interrupted-sync
	// recovery. It is immediately ready because those lifecycle jobs are owned
	// by the `control` (or `standalone`) role.
	lifecycleWG := &sync.WaitGroup{}
	if !cfg.OwnsSyncLifecycle() {
		startupState.MarkReady()
		logger.Infow("serve role startup complete; public routes enabled without lifecycle jobs")
	} else {
		lifecycleWG.Add(1)
		go func() {
			defer lifecycleWG.Done()

			if err := storage.RunMigrations(appCtx, db, cfg); err != nil {
				if errors.Is(err, context.Canceled) {
					logger.Infow("startup migrations cancelled by shutdown")
					return
				}
				logger.Fatalf("failed to run database migrations: %v", err)
			}

			startupState.MarkReady()
			logger.Infow("startup migrations completed; general api routes enabled")

			// Search-index warmup enables persisted Redis indexes for read/search
			// traffic. The pure `control` role never serves public read traffic, so
			// it does not run the local decoded-index warmup; doing so would needlessly
			// decode persisted indexes into control process memory. Persisted indexes
			// are (re)built by sync / force-sync in `control` and by warmup in
			// `standalone`, so this only disables the optional startup decode path for
			// `control` and does not remove any repair behavior.
			if len(masterDataSources) > 0 && cfg.MasterDataWarmSearchIndexes && cfg.Role != config.AppRoleControl {
				lifecycleWG.Add(1)
				go func() {
					defer lifecycleWG.Done()

					warmupTimeout := cfg.MasterDataSyncJobTimeout
					if warmupTimeout <= 0 {
						warmupTimeout = 120
					}
					wctx, wcancel := context.WithTimeout(appCtx, time.Duration(warmupTimeout)*time.Second)
					defer wcancel()

					logger.Infow("master data search index warmup running in background", "regions", len(masterDataSources))
					loadedRegions, rebuiltRegions, warmErr := masterDataSyncUsecase.EnsureConfiguredRegionIndexes(wctx)
					if warmErr != nil {
						if errors.Is(warmErr, context.Canceled) {
							logger.Infow("master data search index warmup cancelled by shutdown")
							return
						}
						logger.Warnw("master data search index warmup completed with errors", "error", warmErr)
						return
					}

					if len(loadedRegions) == 0 && len(rebuiltRegions) == 0 {
						logger.Infow("master data search index warmup found no missing regions")
						return
					}

					logger.Infow(
						"master data search index warmup completed",
						"loaded_regions", loadedRegions,
						"rebuilt_regions", rebuiltRegions,
					)
				}()
			} else if cfg.Role == config.AppRoleControl && cfg.MasterDataWarmSearchIndexes {
				logger.Infow("control role skips local search-index warmup; persisted indexes are built during sync/force-sync")
			}

			if len(masterDataSources) > 0 && (cfg.MasterDataAutoSync || cfg.MasterDataRecoverInterrupted) {
				lifecycleWG.Add(1)
				go func() {
					defer lifecycleWG.Done()

					if cfg.MasterDataRecoverInterrupted {
						interruptedRegions, ierr := masterDataSyncUsecase.InterruptedRegions(appCtx)
						if ierr != nil {
							if errors.Is(ierr, context.Canceled) {
								return
							}
							logger.Warnw("failed to inspect interrupted master data sync status", "error", ierr)
						} else if len(interruptedRegions) > 0 {
							if cfg.MasterDataAutoSync {
								logger.Infow(
									"master data startup sync detected interrupted regions; full startup sync will recover them",
									"regions", interruptedRegions,
									"configured_regions", len(masterDataSources),
								)
							} else {
								logger.Infow("master data interrupted sync recovery running in background", "regions", interruptedRegions)
								if _, recoverErr := masterDataSyncUsecase.RecoverInterruptedSync(appCtx); recoverErr != nil {
									if errors.Is(recoverErr, context.Canceled) {
										return
									}
									logger.Errorw("master data interrupted sync recovery completed with errors", "error", recoverErr, "regions", interruptedRegions)
									return
								}

								logger.Infow("master data interrupted sync recovery completed successfully", "regions", interruptedRegions)
								return
							}
						}
					}

					if !cfg.MasterDataAutoSync {
						return
					}

					logger.Infow("master data startup sync running in background", "regions", len(masterDataSources))
					if syncErr := masterDataSyncUsecase.SyncAll(appCtx); syncErr != nil {
						if errors.Is(syncErr, context.Canceled) {
							return
						}
						logger.Errorw("master data startup sync completed with errors", "error", syncErr)
						return
					}

					logger.Infow("master data startup sync completed successfully", "regions", len(masterDataSources))
				}()
			}
		}()
	}

	// Graceful shutdown wiring. The HTTP server is told to stop accepting new
	// connections first; its shutdown callback then cancels the app lifecycle
	// (interrupting background sync workers) and closes the SSE event hub. The
	// shutdown deadline timer is started only when a SIGTERM/SIGINT is received
	// (see handleShutdownSignals), so the grace period is not consumed at startup.
	server.RegisterOnShutdown(func() {
		// Stop accepting new connections has already happened inside
		// server.Shutdown; now interrupt background workers and release SSE
		// subscribers so long-lived streams do not hold the graceful window open.
		appCancel()
		masterDataEventHub.Close()
	})

	shutdownTimeout := time.Duration(cfg.ShutdownTimeoutSeconds) * time.Second
	if shutdownTimeout <= 0 {
		shutdownTimeout = 25 * time.Second
	}

	sigCh := newShutdownSignalChannel()
	shutdownStartedCh := make(chan shutdownControl, 1)
	httpErrCh := make(chan error, 1)
	go func() {
		httpErrCh <- handleShutdownSignals(sigCh, server, logger, shutdownTimeout, shutdownStartedCh)
	}()
	defer signal.Stop(sigCh)

	// Wait for the server to stop: either a signal-driven graceful shutdown, or a
	// fatal serve error.
	var serverShutdownCtx context.Context
	var serveErr error
	var signaled bool
	var drainCancel context.CancelFunc
	select {
	case serverErr := <-serverErrCh:
		// Unexpected serve failure: do NOT hard-exit here. Cancel the app
		// lifecycle (mirroring the signal-driven OnShutdown callback) so background
		// sync workers are interrupted, then fall through to the controlled
		// teardown path which drains workers and closes dependencies before we
		// return a non-zero status.
		serveErr = serverErr
		appCancel()
		// Server stopped without a signal; still bound the worker drain.
		serverShutdownCtx = context.Background()
	case state := <-shutdownStartedCh:
		// Signal received: server.Shutdown is draining in-flight requests using
		// this same deadline context. Wait for it to finish. The caller now owns
		// the deadline cancel func; it is released after the worker drain below.
		signaled = true
		serverShutdownCtx = state.ctx
		drainCancel = state.cancel
		serverErr := <-serverErrCh
		if serverErr != nil && !errors.Is(serverErr, http.ErrServerClosed) {
			serveErr = serverErr
			logger.Errorw("http server stopped with error after shutdown signal", "error", serverErr)
		}
	}
	logger.Infow("http server stopped accepting requests")

	// In the signal-driven path the handler has returned (its server.Shutdown
	// completed) and reported any HTTP drain timeout. Surface that into runErr so
	// a non-zero exit is returned, while preserving the first-signal deadline
	// origin and the second-signal force-close behavior. In the serve-error path
	// the handler is still blocked waiting for a signal, so skip this read.
	var httpDrainErr error
	if signaled {
		httpDrainErr = <-httpErrCh
	}

	// The worker drain uses a single deadline context owned by main, spanning from
	// first-signal receipt through HTTP shutdown and worker joins. In the signaled
	// path this is the context handed back by the shutdown handler (the same
	// context server.Shutdown used), so the grace period is shared and the workers
	// get the remaining deadline rather than a freshly re-derived window that may
	// already be expired. In the serve-error path (no signal) we fall back to a
	// fresh bounded deadline whose cancel is deferred. main awaits the shutdown
	// handler's result (httpDrainErr) instead of re-deriving from a possibly
	// expired context.
	var workerCtx context.Context
	var workerCancel context.CancelFunc
	if signaled {
		workerCtx = serverShutdownCtx
	} else {
		workerCtx, workerCancel = context.WithTimeout(context.Background(), shutdownTimeout)
		defer workerCancel()
	}

	var runErr error
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		runErr = serveErr
		logger.Errorw("server exited with error", "error", serveErr)
	}
	if httpDrainErr != nil {
		if runErr == nil {
			runErr = httpDrainErr
		}
		logger.Errorw("http graceful drain exceeded deadline", "error", httpDrainErr)
	}

	workerErr := waitForBackgroundWorkers(workerCtx, logger, lifecycleWG, masterDataSyncUsecase, gitHubWebhookHandler, masterDataEventHub)
	if workerErr != nil {
		if runErr == nil {
			runErr = workerErr
		}
		logger.Errorw("graceful shutdown did not complete in time", "error", workerErr)
		// Safe bounded fallback: the shared deadline elapsed before workers
		// stopped. Ensure lifecycle cancellation is propagated so in-flight workers
		// are interrupted before dependency teardown closes the resources they
		// depend on. appCancel is idempotent: in the signal path it was already
		// invoked by the server shutdown callback, and in the serve-error path it
		// was invoked directly above.
		appCancel()
	}

	// The first-signal deadline context was transferred to main so the worker
	// drain could use the remaining grace period. Release its timer now that the
	// drain is complete (only in the signaled path, where main owns it).
	if drainCancel != nil {
		drainCancel()
	}

	logger.Infow("shutting down dependencies")

	// Ordered teardown: stop OTel periodic callbacks/flush first (so no metrics
	// push races with closed dependencies), then Redis cache, then database,
	// then logger flush. Cleanup runs exactly once.
	cleanupObservability()
	if masterDataCacheCloser != nil {
		if closeErr := masterDataCacheCloser(); closeErr != nil {
			logger.Warnw("redis cache close error", "error", closeErr)
		}
	}
	if closeErr := db.Close(); closeErr != nil {
		logger.Warnw("database close error", "error", closeErr)
	}
	cleanupLogger()

	if runErr != nil {
		os.Exit(1)
	}
}

func runMigrationCommand(args []string) (err error) {
	if len(args) > 0 {
		return fmt.Errorf("usage: sekai-master-api migrate")
	}

	cfg := config.Load()
	cleanupLogger, err := logging.Setup(cfg.LogLevel, cfg.IsDevelopment(), cfg.LokiPushURL)
	if err != nil {
		return fmt.Errorf("initialize logging: %w", err)
	}
	defer cleanupLogger()

	db, err := storage.OpenDB(cfg)
	if err != nil {
		return fmt.Errorf("initialize database for migrations: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			if err == nil {
				err = fmt.Errorf("close migration database: %w", closeErr)
			} else {
				err = fmt.Errorf("%w; additionally failed to close migration database: %v", err, closeErr)
			}
		}
	}()

	if err := storage.RunMigrations(context.Background(), db, cfg); err != nil {
		return fmt.Errorf("run database migrations: %w", err)
	}

	zap.S().Infow("database migrations completed")
	return nil
}

// applyRoleSubcommandFromArgs lets the first positional argument select the
// runtime role via `APP_ROLE`, so `sekai-master-api serve`/`control`/`standalone`
// is equivalent to `APP_ROLE=serve`/`control`/`standalone`. Invocation with no
// recognized subcommand (or no args) leaves APP_ROLE unset, which resolves to
// the standalone default defined in config. Unknown subcommands fall back to the
// default rather than failing, keeping current "run without args" behavior.
func applyRoleSubcommandFromArgs() {
	if len(os.Args) < 2 {
		return
	}

	switch config.AppRole(os.Args[1]) {
	case config.AppRoleServe, config.AppRoleControl, config.AppRoleStandalone:
		_ = os.Setenv("APP_ROLE", os.Args[1])
	}
}

func buildMasterDataSources(cfg config.Config) []masterdata.Source {
	sources := make([]masterdata.Source, 0, len(cfg.MasterDataSources))
	for region, source := range cfg.MasterDataSources {
		sources = append(sources, masterdata.Source{
			Region: region,
			Owner:  source.Owner,
			Repo:   source.Repo,
			Ref:    source.Ref,
			Path:   source.Path,
		})
	}

	return sources
}
