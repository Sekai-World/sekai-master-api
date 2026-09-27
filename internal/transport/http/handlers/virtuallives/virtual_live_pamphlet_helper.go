package virtuallives

import (
	"context"

	"sekai-master-api/internal/usecase"
)

func findVirtualLivePamphlet(
	ctx context.Context,
	masterDataSync *usecase.MasterDataSyncUsecase,
	region string,
	virtualLiveID string,
) map[string]any {
	return findVirtualLiveRelatedRecord(ctx, masterDataSync, region, "virtuallivepamphlets", virtualLiveID)
}

func findVirtualLiveTicket(
	ctx context.Context,
	masterDataSync *usecase.MasterDataSyncUsecase,
	region string,
	virtualLiveID string,
) map[string]any {
	return findVirtualLiveRelatedRecord(ctx, masterDataSync, region, "virtuallivetickets", virtualLiveID)
}

func findVirtualLiveRelatedRecord(
	ctx context.Context,
	masterDataSync *usecase.MasterDataSyncUsecase,
	region string,
	entity string,
	virtualLiveID string,
) map[string]any {
	if masterDataSync == nil || virtualLiveID == "" {
		return nil
	}

	matches, err := masterDataSync.ListByIndex(ctx, region, entity, "virtualLiveId", [][]any{{virtualLiveID}})
	if err != nil {
		return nil
	}

	for _, record := range matches[0] {

		pamphlet := make(map[string]any, len(record))
		for key, value := range record {
			if key == "virtualLiveId" {
				continue
			}
			pamphlet[key] = value
		}

		return pamphlet
	}

	return nil
}
