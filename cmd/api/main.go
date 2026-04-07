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
	"sekai-master-api/internal/repository"
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

	db, err := storage.OpenDB(cfg)
	if err != nil {
		logger.Fatalf("failed to initialize database: %v", err)
	}
	defer db.Close()

	if err := storage.RunMigrations(context.Background(), db, cfg); err != nil {
		logger.Fatalf("failed to run database migrations: %v", err)
	}

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
	router := transport.NewRouter(cfg, db, tokenVerifier, masterDataSyncUsecase, masterDataEventHub)

	listener, err := net.Listen("tcp", ":"+cfg.Port)
	if err != nil {
		logger.Fatalf("failed to listen on port %s: %v", cfg.Port, err)
	}
	logger.Infow("api server listening", "addr", listener.Addr().String())

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- router.RunListener(listener)
	}()

	if cfg.MasterDataAutoSync && len(masterDataSources) > 0 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.MasterDataSyncTimeout)*time.Second)
			defer cancel()

			logger.Infow("master data startup sync running in background", "regions", len(masterDataSources))
			if err := masterDataSyncUsecase.SyncAll(ctx); err != nil {
				logger.Errorw("master data startup sync completed with errors", "error", err)
				return
			}

			logger.Infow("master data startup sync completed successfully", "regions", len(masterDataSources))
		}()
	}

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
