package main

import (
	"context"
	"net"
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
	"sekai-master-api/internal/usecase"
)

// @title sekai-master-api
// @version 1.0
// @description API for master data sync and card querying.
// @BasePath /api/v1
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization

func main() {
	cfg := config.Load()

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
	defer cleanupObservability()
	if err := observability.RegisterRuntimeMetrics(); err != nil {
		logger.Fatalf("failed to register runtime metrics: %v", err)
	}

	db, err := storage.OpenDB(cfg)
	if err != nil {
		logger.Fatalf("failed to initialize database: %v", err)
	}
	defer db.Close()

	tokenVerifier, err := auth.NewOIDCVerifier(context.Background(), cfg)
	if err != nil {
		logger.Fatalf("failed to initialize oidc verifier: %v", err)
	}

	masterDataSources := buildMasterDataSources(cfg)
	masterDataStatusRepository := repository.NewMasterDataSyncStatusRepository(db, cfg.DatabaseDriver())
	masterDataLoader := repository.NewGitHubMasterDataRepository(
		time.Duration(cfg.MasterDataHTTPTimeout)*time.Second,
		cfg.MasterDataGitHubToken,
		cfg.MasterDataFileConcurrency,
		cfg.MasterDataHTTPRetryCount,
		time.Duration(cfg.MasterDataHTTPRetryBackoffMS)*time.Millisecond,
	)
	masterDataEventHub := usecase.NewMasterDataEventHub()
	masterDataCache, err := storage.NewRedisMasterDataCache(cfg)
	if err != nil {
		logger.Fatalf("failed to initialize redis master data cache: %v", err)
	}

	masterDataCacheCloser := masterDataCache.Close
	defer func() {
		if masterDataCacheCloser != nil {
			_ = masterDataCacheCloser()
		}
	}()

	masterDataSyncUsecase := usecase.NewMasterDataSyncUsecase(
		masterDataSources,
		masterDataLoader,
		masterDataCache,
		masterDataStatusRepository,
		masterDataEventHub,
		cfg.MasterDataSyncConcurrency,
	)
	masterDataSyncUsecase.SetRegionTimeout(time.Duration(cfg.MasterDataSyncTimeout) * time.Second)
	masterDataSyncUsecase.EnableDevelopmentBackupBootstrap(cfg.IsDevelopment())
	if err := observability.RegisterMasterDataMetrics(masterDataSyncUsecase, masterDataCache); err != nil {
		logger.Fatalf("failed to register master data metrics: %v", err)
	}
	startupState := startup.NewState()
	router, err := transport.NewRouter(cfg, db, tokenVerifier, masterDataSyncUsecase, masterDataEventHub, startupState)
	if err != nil {
		logger.Fatalf("failed to initialize router: %v", err)
	}

	listener, err := net.Listen("tcp", ":"+cfg.Port)
	if err != nil {
		logger.Fatalf("failed to listen on port %s: %v", cfg.Port, err)
	}
	logger.Infow("api server listening", "addr", listener.Addr().String())

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- router.RunListener(listener)
	}()

	go func() {
		if err := storage.RunMigrations(context.Background(), db, cfg); err != nil {
			logger.Fatalf("failed to run database migrations: %v", err)
		}

		startupState.MarkReady()
		logger.Infow("startup migrations completed; general api routes enabled")

		if len(masterDataSources) > 0 && cfg.MasterDataWarmSearchIndexes {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.MasterDataSyncTimeout)*time.Second)
				defer cancel()

				logger.Infow("master data search index warmup running in background", "regions", len(masterDataSources))
				loadedRegions, rebuiltRegions, warmErr := masterDataSyncUsecase.EnsureConfiguredRegionIndexes(ctx)
				if warmErr != nil {
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
		}

		if len(masterDataSources) > 0 && (cfg.MasterDataAutoSync || cfg.MasterDataRecoverInterrupted) {
			go func() {
				if cfg.MasterDataRecoverInterrupted {
					interruptedRegions, err := masterDataSyncUsecase.InterruptedRegions(context.Background())
					if err != nil {
						logger.Warnw("failed to inspect interrupted master data sync status", "error", err)
					} else if len(interruptedRegions) > 0 {
						if cfg.MasterDataAutoSync {
							logger.Infow(
								"master data startup sync detected interrupted regions; full startup sync will recover them",
								"regions", interruptedRegions,
								"configured_regions", len(masterDataSources),
							)
						} else {
							logger.Infow("master data interrupted sync recovery running in background", "regions", interruptedRegions)
							if _, recoverErr := masterDataSyncUsecase.RecoverInterruptedSync(context.Background()); recoverErr != nil {
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
				if err := masterDataSyncUsecase.SyncAll(context.Background()); err != nil {
					logger.Errorw("master data startup sync completed with errors", "error", err)
					return
				}

				logger.Infow("master data startup sync completed successfully", "regions", len(masterDataSources))
			}()
		}
	}()

	if err := <-serverErrCh; err != nil {
		logger.Fatalf("server exited with error: %v", err)
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
