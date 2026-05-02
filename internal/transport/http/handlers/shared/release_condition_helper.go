package shared

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"sekai-master-api/internal/tracing"
	"sekai-master-api/internal/usecase"
)

func BuildRecordWithReleaseCondition(
	ctx context.Context,
	masterDataSync *usecase.MasterDataSyncUsecase,
	region string,
	record map[string]any,
) map[string]any {
	ctx, span := tracing.StartSpan(ctx, "master_data.expand_release_condition", attribute.String("region", strings.ToLower(strings.TrimSpace(region))))
	var spanErr error
	defer func() {
		tracing.EndSpan(span, spanErr)
	}()

	if record == nil {
		return map[string]any{}
	}

	result := make(map[string]any, len(record))
	for key, value := range record {
		result[key] = value
	}

	rawReleaseConditionID, hasReleaseConditionID := result["releaseConditionId"]
	if !hasReleaseConditionID {
		span.SetAttributes(attribute.Bool("release_condition.present", false))
		return result
	}

	delete(result, "releaseConditionId")
	span.SetAttributes(attribute.Bool("release_condition.present", true))

	releaseConditionLookupID := NormalizeAnyID(rawReleaseConditionID)
	if masterDataSync == nil || releaseConditionLookupID == "" {
		result["releaseCondition"] = nil
		return result
	}

	releaseCondition, found, err := masterDataSync.GetByID(ctx, region, "releaseconditions", releaseConditionLookupID)
	if err != nil || !found {
		spanErr = err
		span.SetAttributes(attribute.Bool("release_condition.found", false))
		result["releaseCondition"] = nil
		return result
	}

	span.SetAttributes(attribute.Bool("release_condition.found", true))
	result["releaseCondition"] = releaseCondition
	return result
}
