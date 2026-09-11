package lookups

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
)

func (handler *LookupHandler) prepareCharacterBatch(c *gin.Context, entity string, limit int, requiredMessage string) (string, []int64, bool) {
	if handler == nil || handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return "", nil, false
	}

	region, ids, ok := parseCharacterBatchRequest(c, limit, requiredMessage)
	if !ok {
		return "", nil, false
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, entity) {
		return "", nil, false
	}

	return region, ids, true
}

func parseCharacterBatchRequest(c *gin.Context, limit int, requiredMessage string) (string, []int64, bool) {
	region := strings.TrimSpace(c.Param("region"))
	parts := strings.Split(c.Query("ids"), ",")
	if region == "" || len(parts) == 0 || len(parts) > limit {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", requiredMessage)
		return "", nil, false
	}

	ids := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "ids must contain positive integers")
			return "", nil, false
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	return region, ids, true
}

func loadCharacterBatchItems[T any](handler *LookupHandler, ctx context.Context, region string, entity string, ids []int64, build func(int64, map[string]any) (T, bool)) ([]T, []int64, error) {
	items := make([]T, 0, len(ids))
	missingIDs := make([]int64, 0)
	for _, id := range ids {
		record, found, err := handler.masterDataSync.GetByID(ctx, region, entity, strconv.FormatInt(id, 10))
		if err != nil {
			return nil, nil, err
		}
		if !found {
			missingIDs = append(missingIDs, id)
			continue
		}

		item, ok := build(id, record)
		if !ok {
			missingIDs = append(missingIDs, id)
			continue
		}
		items = append(items, item)
	}

	return items, missingIDs, nil
}
