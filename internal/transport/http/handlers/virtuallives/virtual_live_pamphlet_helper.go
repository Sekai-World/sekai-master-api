package virtuallives

import (
	"context"

	"sekai-master-api/internal/transport/http/handlers/shared"
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

	matches, err := masterDataSync.Search(ctx, region, entity, virtualLiveID, []string{"virtualLiveId"}, 10)
	if err != nil {
		return nil
	}

	targetVirtualLiveID := shared.NormalizeAnyID(virtualLiveID)
	for _, match := range matches {
		if shared.NormalizeAnyID(match.Item["virtualLiveId"]) != targetVirtualLiveID {
			continue
		}

		pamphlet := make(map[string]any, len(match.Item))
		for key, value := range match.Item {
			if key == "virtualLiveId" {
				continue
			}
			pamphlet[key] = value
		}

		return pamphlet
	}

	return nil
}
