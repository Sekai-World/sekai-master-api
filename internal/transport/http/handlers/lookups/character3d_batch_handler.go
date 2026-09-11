package lookups

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
)

const character3DBatchLimit = 100

func normalizePositiveInt64(value any) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(shared.NormalizeAnyID(value)), 10, 64)
	return id, err == nil && id > 0
}

// Character3DsBatch godoc
// @Summary Get Character3D mappings by IDs
// @Tags character3ds
// @Produce json
// @Param region path string true "Region"
// @Param ids query string true "Comma-separated Character3D IDs (up to 100)"
// @Success 200 {object} shared.Character3DBatchResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Router /character3ds/{region}/batch [get]
func (handler *LookupHandler) Character3DsBatch(c *gin.Context) {
	if handler == nil || handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region, ids, ok := parseCharacterBatchRequest(c, character3DBatchLimit, "region and 1 to 100 character3d ids are required")
	if !ok {
		return
	}

	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, "character3ds") {
		return
	}

	items := make([]shared.Character3DBatchItem, 0, len(ids))
	missingIDs := make([]int64, 0)
	for _, id := range ids {
		record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "character3ds", strconv.FormatInt(id, 10))
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "CHARACTER_3D_QUERY_ERROR", "failed to query character3d records")
			return
		}
		if !found {
			missingIDs = append(missingIDs, id)
			continue
		}
		gameCharacterID, ok := normalizePositiveInt64(record["characterId"])
		if !ok {
			missingIDs = append(missingIDs, id)
			continue
		}
		items = append(items, shared.Character3DBatchItem{
			ID: id, GameCharacterID: gameCharacterID,
			Unit: strings.TrimSpace(shared.NormalizeAnyID(record["unit"])),
			Name: strings.TrimSpace(shared.NormalizeAnyID(record["name"])),
		})
	}

	response.JSON(c, http.StatusOK, shared.Character3DBatchResponse{Items: items, MissingIDs: missingIDs})
}
