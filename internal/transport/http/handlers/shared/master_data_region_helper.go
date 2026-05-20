package shared

import (
	"context"

	"sekai-master-api/internal/usecase"
)

func ReadyMasterDataRegions(ctx context.Context, masterDataSync *usecase.MasterDataSyncUsecase) ([]string, error) {
	if masterDataSync == nil {
		return nil, nil
	}

	return masterDataSync.ReadyRegions(ctx)
}

func AvailableRegionsByID(ctx context.Context, masterDataSync *usecase.MasterDataSyncUsecase, entity string, id string) ([]string, error) {
	readyRegions, err := ReadyMasterDataRegions(ctx, masterDataSync)
	if err != nil {
		return nil, err
	}

	regions := make([]string, 0, len(readyRegions))
	for _, region := range readyRegions {
		_, found, err := masterDataSync.GetByID(ctx, region, entity, id)
		if err != nil {
			return nil, err
		}
		if found {
			regions = append(regions, region)
		}
	}

	return regions, nil
}
