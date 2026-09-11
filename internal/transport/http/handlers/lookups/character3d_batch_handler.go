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

const character3DBatchLimit = 100

func normalizePositiveInt64(value any) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(shared.NormalizeAnyID(value)), 10, 64)
	return id, err == nil && id > 0
}

func buildCharacter3DBatchItem(id int64, record map[string]any) (shared.Character3DBatchItem, bool) {
	gameCharacterID, ok := normalizePositiveInt64(record["characterId"])
	if !ok {
		return shared.Character3DBatchItem{}, false
	}

	return shared.Character3DBatchItem{
		ID:              id,
		GameCharacterID: gameCharacterID,
		Unit:            strings.TrimSpace(shared.NormalizeAnyID(record["unit"])),
		Name:            strings.TrimSpace(shared.NormalizeAnyID(record["name"])),
	}, true
}

func (handler *LookupHandler) loadCharacter3DBatchItems(ctx context.Context, region string, ids []int64) ([]shared.Character3DBatchItem, []int64, error) {
	return loadCharacterBatchItems(handler, ctx, region, "character3ds", ids, buildCharacter3DBatchItem)
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
	region, ids, ok := handler.prepareCharacterBatch(c, "character3ds", character3DBatchLimit, "region and 1 to 100 character3d ids are required")
	if !ok {
		return
	}

	items, missingIDs, err := handler.loadCharacter3DBatchItems(c.Request.Context(), region, ids)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CHARACTER_3D_QUERY_ERROR", "failed to query character3d records")
		return
	}

	response.JSON(c, http.StatusOK, shared.Character3DBatchResponse{Items: items, MissingIDs: missingIDs})
}
