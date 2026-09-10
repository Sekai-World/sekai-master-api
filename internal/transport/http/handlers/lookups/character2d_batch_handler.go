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

const character2DBatchLimit = 100

func optionalBool(record map[string]any, key string) *bool {
	value, ok := record[key].(bool)
	if !ok {
		return nil
	}
	return &value
}

func (handler *LookupHandler) resolveCharacter2DDisplayName(ctx context.Context, region string, characterType string, characterID int64) string {
	entity := ""
	switch characterType {
	case "", "game_character":
		entity = "gamecharacters"
	case "mob":
		entity = "mobcharacters"
	case "sub_game_character":
		entity = "subgamecharacters"
	default:
		return ""
	}

	character, found, err := handler.masterDataSync.GetByID(ctx, region, entity, strconv.FormatInt(characterID, 10))
	if err != nil || !found {
		return ""
	}

	if entity != "gamecharacters" {
		return strings.TrimSpace(shared.NormalizeAnyID(character["name"]))
	}

	nameParts := make([]string, 0, 2)
	for _, key := range []string{"firstName", "givenName"} {
		if name := strings.TrimSpace(shared.NormalizeAnyID(character[key])); name != "" {
			nameParts = append(nameParts, name)
		}
	}
	return strings.Join(nameParts, " ")
}

// Character2DsBatch godoc
// @Summary Get Character2D mappings by IDs
// @Tags character2ds
// @Produce json
// @Param region path string true "Region"
// @Param ids query string true "Comma-separated Character2D IDs (up to 100)"
// @Success 200 {object} shared.Character2DBatchResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Router /character2ds/{region}/batch [get]
func (handler *LookupHandler) Character2DsBatch(c *gin.Context) {
	if handler == nil || handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	parts := strings.Split(c.Query("ids"), ",")
	if region == "" || len(parts) == 0 || len(parts) > character2DBatchLimit {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region and 1 to 100 character2d ids are required")
		return
	}

	ids := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "ids must contain positive integers")
			return
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, "character2ds") {
		return
	}

	items := make([]shared.Character2DBatchItem, 0, len(ids))
	missingIDs := make([]int64, 0)
	for _, id := range ids {
		record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "character2ds", strconv.FormatInt(id, 10))
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "CHARACTER_2D_QUERY_ERROR", "failed to query character2d records")
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
		characterType := strings.TrimSpace(shared.NormalizeAnyID(record["characterType"]))
		item := shared.Character2DBatchItem{
			ID:                   id,
			GameCharacterID:      gameCharacterID,
			CharacterType:        characterType,
			Unit:                 strings.TrimSpace(shared.NormalizeAnyID(record["unit"])),
			AssetName:            strings.TrimSpace(shared.NormalizeAnyID(record["assetName"])),
			IsNextGrade:          optionalBool(record, "isNextGrade"),
			IsEnabledFlipDisplay: optionalBool(record, "isEnabledFlipDisplay"),
		}
		if displayName := handler.resolveCharacter2DDisplayName(c.Request.Context(), region, characterType, gameCharacterID); displayName != "" {
			item.DisplayName = displayName
		}
		items = append(items, item)
	}

	response.JSON(c, http.StatusOK, shared.Character2DBatchResponse{Items: items, MissingIDs: missingIDs})
}
