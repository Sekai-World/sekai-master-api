package shared

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

func ReadyMasterDataRegions(ctx context.Context, masterDataSync *usecase.MasterDataSyncUsecase) ([]string, error) {
	if masterDataSync == nil {
		return nil, nil
	}

	return masterDataSync.ReadyRegions(ctx)
}

func RegionHasEntityRecordsOrReady(ctx context.Context, masterDataSync *usecase.MasterDataSyncUsecase, region string, entity string) (bool, error) {
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
	if hasRecords {
		hasSuccessfulSync, err := masterDataSync.HasSuccessfulSync(ctx, normalizedRegion)
		if err != nil {
			return false, err
		}
		if hasSuccessfulSync {
			return true, nil
		}
	}

	readyRegions, err := ReadyMasterDataRegions(ctx, masterDataSync)
	if err != nil {
		return false, err
	}

	for _, readyRegion := range readyRegions {
		if readyRegion == normalizedRegion {
			return true, nil
		}
	}

	return false, nil
}

func EnsureRegionReadyForEntityRecords(c *gin.Context, masterDataSync *usecase.MasterDataSyncUsecase, region string, entity string) bool {
	if masterDataSync == nil {
		return true
	}

	ready, err := RegionHasEntityRecordsOrReady(c.Request.Context(), masterDataSync, region, entity)
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
