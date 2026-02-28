package main

import (
	"context"
	"log"
	"time"

	"sekai-master-api/internal/auth"
	"sekai-master-api/internal/config"
	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/repository"
	"sekai-master-api/internal/storage"
	transport "sekai-master-api/internal/transport/http"
	"sekai-master-api/internal/usecase"
)

func main() {
	cfg := config.Load()

	db, err := storage.OpenDB(cfg)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	defer db.Close()

	keycloakVerifier, err := auth.NewKeycloakVerifier(context.Background(), cfg)
	if err != nil {
		log.Fatalf("failed to initialize keycloak verifier: %v", err)
	}

	masterDataSources := buildMasterDataSources(cfg)
	masterDataStatusRepository := repository.NewMasterDataSyncStatusRepository(db)
	masterDataLoader := repository.NewGitHubMasterDataRepository(time.Duration(cfg.MasterDataHTTPTimeout)*time.Second, cfg.MasterDataGitHubToken)
	masterDataCache, masterDataCacheCloser := buildMasterDataCache(cfg)
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
	)

	if cfg.MasterDataAutoSync && len(masterDataSources) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.MasterDataSyncTimeout)*time.Second)
		defer cancel()
		if err := masterDataSyncUsecase.SyncAll(ctx); err != nil {
			log.Printf("master data startup sync completed with errors: %v", err)
		} else {
			log.Printf("master data startup sync completed successfully for %d region(s)", len(masterDataSources))
		}
	}

	router := transport.NewRouter(cfg, db, keycloakVerifier, masterDataSyncUsecase)

	if err := router.Run(":" + cfg.Port); err != nil {
		log.Fatalf("server exited with error: %v", err)
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

func buildMasterDataCache(cfg config.Config) (usecase.MasterDataCache, func() error) {
	if cfg.MasterDataCacheBackend == "redis" {
		redisCache, err := storage.NewRedisMasterDataCache(cfg)
		if err == nil {
			return redisCache, redisCache.Close
		}
		log.Printf("master data redis cache unavailable, fallback to memory cache: %v", err)
	}

	return repository.NewMemoryMasterDataCache(), nil
}
