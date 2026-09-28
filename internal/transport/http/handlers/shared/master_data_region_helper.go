package shared

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

// RegionReadyForEntity reports whether region's data is ready for an entity:
// the region's latest sync status is success, and the store holds records of
// the entity or, for an entity the region's source does not have, any records
// of the region. A region whose store is empty stays not ready, so reads never
// answer from a store that sync has not filled yet.
func RegionReadyForEntity(ctx context.Context, masterDataSync *usecase.MasterDataSyncUsecase, region string, entity string) (bool, error) {
	if masterDataSync == nil {
		return true, nil
	}

	normalizedRegion := strings.ToLower(strings.TrimSpace(region))
	normalizedEntity := strings.ToLower(strings.TrimSpace(entity))
	if normalizedRegion == "" || normalizedEntity == "" {
		return false, nil
	}

	hasRecords, err := masterDataSync.HasEntityRecords(ctx, normalizedRegion, normalizedEntity)
	if err != nil {
		return false, err
	}
	if !hasRecords {
		hasRecords, err = masterDataSync.HasRegionData(ctx, normalizedRegion)
		if err != nil || !hasRecords {
			return false, err
		}
	}
	return masterDataSync.HasSuccessfulSync(ctx, normalizedRegion)
}

func EnsureRegionReadyForEntityRecords(c *gin.Context, masterDataSync *usecase.MasterDataSyncUsecase, region string, entity string) bool {
	if masterDataSync == nil {
		return true
	}

	ready, err := RegionReadyForEntity(c.Request.Context(), masterDataSync, region, entity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MASTER_DATA_STATUS_ERROR", "failed to check master data sync status")
		return false
	}
	if ready {
		return true
	}

	response.Error(c, http.StatusServiceUnavailable, "REGION_DATA_NOT_READY", "region data is updating or unavailable, please try again later")
	return false
}

func AvailableRegionsByID(ctx context.Context, masterDataSync *usecase.MasterDataSyncUsecase, entity string, id string) ([]string, error) {
	if masterDataSync == nil {
		return nil, nil
	}

	regions, err := masterDataSync.SuccessfulSyncRegions(ctx)
	if err != nil {
		return nil, err
	}

	availableRegions := make([]string, 0, len(regions))
	for _, region := range regions {
		_, found, err := masterDataSync.GetByID(ctx, region, entity, id)
		if err != nil {
			return nil, err
		}
		if found {
			availableRegions = append(availableRegions, region)
		}
	}

	return availableRegions, nil
}
