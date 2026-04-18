package shared

import (
	"context"
	"sort"
	"strings"

	"sekai-master-api/internal/usecase"
)

func ReadyMasterDataRegions(ctx context.Context, masterDataSync *usecase.MasterDataSyncUsecase) ([]string, error) {
	if masterDataSync == nil {
		return nil, nil
	}

	statuses, err := masterDataSync.Status(ctx)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(statuses))
	regions := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if !strings.EqualFold(strings.TrimSpace(status.Status), "success") {
			continue
		}

		region := strings.ToLower(strings.TrimSpace(status.Region))
		if region == "" {
			continue
		}
		if _, exists := seen[region]; exists {
			continue
		}

		seen[region] = struct{}{}
		regions = append(regions, region)
	}

	sort.Strings(regions)
	return regions, nil
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
