package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"sekai-master-api/internal/domain/masterdata"
)

type MasterDataSourceLoader interface {
	LoadRegion(ctx context.Context, source masterdata.Source) (map[string]any, error)
}

type MasterDataCache interface {
	StoreRegion(ctx context.Context, region string, payload map[string]any) error
}

type MasterDataSyncStatusStore interface {
	EnsureSchema(ctx context.Context) error
	Save(ctx context.Context, status masterdata.SyncStatus) error
	List(ctx context.Context) ([]masterdata.SyncStatus, error)
}

type MasterDataSyncUsecase struct {
	sources     []masterdata.Source
	loader      MasterDataSourceLoader
	cache       MasterDataCache
	statusStore MasterDataSyncStatusStore
}

func NewMasterDataSyncUsecase(
	sources []masterdata.Source,
	loader MasterDataSourceLoader,
	cache MasterDataCache,
	statusStore MasterDataSyncStatusStore,
) *MasterDataSyncUsecase {
	return &MasterDataSyncUsecase{
		sources:     sources,
		loader:      loader,
		cache:       cache,
		statusStore: statusStore,
	}
}

func (usecase *MasterDataSyncUsecase) SyncAll(ctx context.Context) error {
	if usecase.statusStore != nil {
		if err := usecase.statusStore.EnsureSchema(ctx); err != nil {
			return err
		}
	}

	var syncErrors []error
	for _, source := range usecase.sources {
		startedAt := time.Now().UTC()
		now := time.Now().UTC()

		payload, err := usecase.loader.LoadRegion(ctx, source)
		if err != nil {
			syncErrors = append(syncErrors, err)
			usecase.saveStatus(ctx, masterdata.SyncStatus{
				Region:         source.Region,
				Status:         "failed",
				FileCount:      0,
				SyncDurationMS: time.Since(startedAt).Milliseconds(),
				LastSyncedAt:   now,
				ErrorMessage:   err.Error(),
				Source:         source,
				UpdatedAt:      now,
			})
			continue
		}

		if err := usecase.cache.StoreRegion(ctx, source.Region, payload); err != nil {
			syncErrors = append(syncErrors, err)
			usecase.saveStatus(ctx, masterdata.SyncStatus{
				Region:         source.Region,
				Status:         "failed",
				FileCount:      len(payload),
				SyncDurationMS: time.Since(startedAt).Milliseconds(),
				LastSyncedAt:   now,
				ErrorMessage:   err.Error(),
				Source:         source,
				UpdatedAt:      now,
			})
			continue
		}

		usecase.saveStatus(ctx, masterdata.SyncStatus{
			Region:         source.Region,
			Status:         "success",
			FileCount:      len(payload),
			SyncDurationMS: time.Since(startedAt).Milliseconds(),
			LastSyncedAt:   now,
			Source:         source,
			UpdatedAt:      now,
		})
	}

	if len(syncErrors) > 0 {
		return errors.Join(syncErrors...)
	}

	return nil
}

func (usecase *MasterDataSyncUsecase) Status(ctx context.Context) ([]masterdata.SyncStatus, error) {
	if usecase.statusStore == nil {
		return nil, nil
	}

	return usecase.statusStore.List(ctx)
}

func (usecase *MasterDataSyncUsecase) saveStatus(ctx context.Context, status masterdata.SyncStatus) {
	if usecase.statusStore == nil {
		return
	}

	if err := usecase.statusStore.Save(ctx, status); err != nil {
		status.ErrorMessage = strings.TrimSpace(status.ErrorMessage + "; failed to persist status: " + err.Error())
	}
}

func BuildMasterDataSources(cfgSources map[string]struct {
	Region string
	Owner  string
	Repo   string
	Ref    string
	Path   string
}) []masterdata.Source {
	sources := make([]masterdata.Source, 0, len(cfgSources))
	for region, source := range cfgSources {
		normalizedRegion := strings.ToLower(strings.TrimSpace(region))
		if normalizedRegion == "" {
			continue
		}

		sources = append(sources, masterdata.Source{
			Region: normalizedRegion,
			Owner:  source.Owner,
			Repo:   source.Repo,
			Ref:    source.Ref,
			Path:   source.Path,
		})
	}

	return sources
}

func ValidateMasterDataSources(sources []masterdata.Source) error {
	for _, source := range sources {
		if strings.TrimSpace(source.Region) == "" {
			return fmt.Errorf("master data source region is required")
		}
		if strings.TrimSpace(source.Owner) == "" {
			return fmt.Errorf("master data source owner is required for region %s", source.Region)
		}
		if strings.TrimSpace(source.Repo) == "" {
			return fmt.Errorf("master data source repo is required for region %s", source.Region)
		}
		if strings.TrimSpace(source.Ref) == "" {
			return fmt.Errorf("master data source ref is required for region %s", source.Region)
		}
	}

	return nil
}
