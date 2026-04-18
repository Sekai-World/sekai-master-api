package shared

import (
	"context"

	"sekai-master-api/internal/usecase"
)

func BuildRecordWithReleaseCondition(
	ctx context.Context,
	masterDataSync *usecase.MasterDataSyncUsecase,
	region string,
	record map[string]any,
) map[string]any {
	if record == nil {
		return map[string]any{}
	}

	result := make(map[string]any, len(record))
	for key, value := range record {
		result[key] = value
	}

	rawReleaseConditionID, hasReleaseConditionID := result["releaseConditionId"]
	if !hasReleaseConditionID {
		return result
	}

	delete(result, "releaseConditionId")

	releaseConditionLookupID := NormalizeAnyID(rawReleaseConditionID)
	if masterDataSync == nil || releaseConditionLookupID == "" {
		result["releaseCondition"] = nil
		return result
	}

	releaseCondition, found, err := masterDataSync.GetByID(ctx, region, "releaseconditions", releaseConditionLookupID)
	if err != nil || !found {
		result["releaseCondition"] = nil
		return result
	}

	result["releaseCondition"] = releaseCondition
	return result
}
