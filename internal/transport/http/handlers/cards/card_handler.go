package cards

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type CardHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
}

var sortableCardFields = []string{
	"id",
	"seq",
	"attr",
	"supportUnit",
	"cardSkillName",
	"prefix",
	"assetbundleName",
	"gachaPhrase",
	"flavorText",
	"releaseAt",
	"archivePublishedAt",
	"initialSpecialTrainingStatus",
}

func NewCardHandler(masterDataSync *usecase.MasterDataSyncUsecase) *CardHandler {
	return &CardHandler{masterDataSync: masterDataSync}
}

// ByID godoc
// @Summary Get card basic info by id
// @Tags cards
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Card ID"
// @Success 200 {object} shared.CardObjectResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /cards/{region}/{id} [get]
func (handler *CardHandler) ByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	id := strings.TrimSpace(c.Param("id"))
	if region == "" || id == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region and id are required")
		return
	}
	if !handler.ensureRegionReady(c, region) {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "cards", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "CARD_NOT_FOUND", "card not found")
		return
	}

	response.JSON(c, http.StatusOK, handler.buildCardBase(c.Request.Context(), region, record))
}

// AvailableRegionsByID godoc
// @Summary Get available regions for a card id
// @Tags cards
// @Produce json
// @Param id path string true "Card ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /cards/regions/{id}/availability [get]
func (handler *CardHandler) AvailableRegionsByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "id is required")
		return
	}

	regions, err := shared.AvailableRegionsByID(c.Request.Context(), handler.masterDataSync, "cards", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card available regions")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{"regions": regions})
}

// ParamsByID godoc
// @Summary Get card params by id
// @Tags cards
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Card ID"
// @Success 200 {object} shared.CardObjectResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /cards/{region}/{id}/params [get]
func (handler *CardHandler) ParamsByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	id := strings.TrimSpace(c.Param("id"))
	if region == "" || id == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region and id are required")
		return
	}
	if !handler.ensureRegionReady(c, region) {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "cards", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card params")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "CARD_NOT_FOUND", "card not found")
		return
	}

	response.JSON(c, http.StatusOK, buildCardParams(record))
}

// EpisodesByID godoc
// @Summary Get card episodes by card id
// @Tags cards
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Card ID"
// @Success 200 {object} shared.CardListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /cards/{region}/{id}/episodes [get]
func (handler *CardHandler) EpisodesByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	id := strings.TrimSpace(c.Param("id"))
	if region == "" || id == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region and id are required")
		return
	}
	if !handler.ensureRegionReady(c, region) {
		return
	}

	_, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "cards", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "CARD_NOT_FOUND", "card not found")
		return
	}

	matches, err := handler.masterDataSync.Search(c.Request.Context(), region, "cardepisodes", id, []string{"cardId"}, 1000000)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card episodes")
		return
	}

	items := make([]map[string]any, 0, len(matches))
	targetCardID := shared.NormalizeAnyID(id)
	for _, match := range matches {
		if shared.NormalizeAnyID(match.Item["cardId"]) != targetCardID {
			continue
		}
		items = append(items, shared.BuildRecordWithReleaseCondition(c.Request.Context(), handler.masterDataSync, region, match.Item))
	}

	response.JSON(c, http.StatusOK, gin.H{
		"items": items,
	})
}

// SearchByPrefix godoc
// @Summary Search cards by prefix
// @Tags cards
// @Produce json
// @Param region path string true "Region"
// @Param q query string true "Prefix query"
// @Param field query string false "Search field (name|skill), default=name"
// @Param page query int false "Page number"
// @Param limit query int false "Max results"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.CardListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /cards/{region}/search [get]
func (handler *CardHandler) SearchByPrefix(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	query := strings.TrimSpace(c.Query("q"))
	if region == "" || query == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region and q are required")
		return
	}
	if !handler.ensureRegionReady(c, region) {
		return
	}

	field := strings.ToLower(strings.TrimSpace(c.Query("field")))
	if field == "" {
		field = "name"
	}

	searchFields := []string{"prefix"}
	switch field {
	case "name":
		searchFields = []string{"prefix"}
	case "skill":
		searchFields = []string{"cardSkillName"}
	default:
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "field must be one of: name, skill")
		return
	}

	page := 1
	if rawPage := strings.TrimSpace(c.Query("page")); rawPage != "" {
		parsedPage, err := strconv.Atoi(rawPage)
		if err != nil || parsedPage <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page must be a positive integer")
			return
		}
		page = parsedPage
	}

	limit := 20
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "limit must be a positive integer")
			return
		}
		limit = parsedLimit
	}

	sortOptions, ok := shared.ParseListSortOptions(c)
	if !ok {
		return
	}

	fetchLimit := 1000000

	matches, err := handler.masterDataSync.Search(c.Request.Context(), region, "cards", query, searchFields, fetchLimit)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to search cards")
		return
	}

	if sortOptions.Enabled {
		records := make([]map[string]any, 0, len(matches))
		for _, match := range matches {
			records = append(records, match.Item)
		}
		if !shared.ValidateSortField(c, sortOptions.Field, records, sortableCardFields) {
			return
		}
		shared.SortResponseItems(records, sortOptions.Field, sortOptions.Descending)
		pagedRecords, pagination := shared.PaginateItems(records, page, limit)
		items := make([]map[string]any, 0, len(pagedRecords))
		for _, record := range pagedRecords {
			items = append(items, handler.buildCardBase(c.Request.Context(), region, record))
		}
		response.JSON(c, http.StatusOK, gin.H{
			"items":      items,
			"pagination": pagination,
		})
		return
	}

	total := len(matches)
	start := (page - 1) * limit
	if start >= total {
		_, pagination := shared.PaginateItems([]map[string]any{}, page, limit)
		pagination["total"] = total
		if limit > 0 {
			pagination["total_pages"] = (total + limit - 1) / limit
		}
		response.JSON(c, http.StatusOK, gin.H{
			"items":      []map[string]any{},
			"pagination": pagination,
		})
		return
	}

	end := start + limit
	if end > total {
		end = total
	}
	pagedMatches := matches[start:end]

	items := make([]map[string]any, 0, len(pagedMatches))
	for _, match := range pagedMatches {
		items = append(items, handler.buildCardBase(c.Request.Context(), region, match.Item))
	}

	totalPages := 0
	if limit > 0 {
		totalPages = (total + limit - 1) / limit
	}
	hasNext := page < totalPages

	response.JSON(c, http.StatusOK, gin.H{
		"items": items,
		"pagination": gin.H{
			"page":        page,
			"page_size":   limit,
			"total":       total,
			"total_pages": totalPages,
			"has_next":    hasNext,
		},
	})
}

// List godoc
// @Summary List cards by page
// @Tags cards
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number"
// @Param page_size query int false "Page size"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.CardListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /cards/{region}/list [get]
func (handler *CardHandler) List(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	if region == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region is required")
		return
	}
	if !handler.ensureRegionReady(c, region) {
		return
	}

	page := 1
	if rawPage := strings.TrimSpace(c.Query("page")); rawPage != "" {
		parsedPage, err := strconv.Atoi(rawPage)
		if err != nil || parsedPage <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page must be a positive integer")
			return
		}
		page = parsedPage
	}

	pageSize := 20
	if rawPageSize := strings.TrimSpace(c.Query("page_size")); rawPageSize != "" {
		parsedPageSize, err := strconv.Atoi(rawPageSize)
		if err != nil || parsedPageSize <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page_size must be a positive integer")
			return
		}
		pageSize = parsedPageSize
	}

	sortOptions, ok := shared.ParseListSortOptions(c)
	if !ok {
		return
	}

	if sortOptions.Enabled {
		records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, "cards")
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to list cards")
			return
		}
		if !shared.ValidateSortField(c, sortOptions.Field, records, sortableCardFields) {
			return
		}
		shared.SortResponseItems(records, sortOptions.Field, sortOptions.Descending)
		pagedRecords, pagination := shared.PaginateItems(records, page, pageSize)
		items := make([]map[string]any, 0, len(pagedRecords))
		for _, record := range pagedRecords {
			items = append(items, handler.buildCardBase(c.Request.Context(), region, record))
		}
		response.JSON(c, http.StatusOK, gin.H{
			"items":      items,
			"pagination": pagination,
		})
		return
	}

	records, total, err := handler.masterDataSync.ListByPage(c.Request.Context(), region, "cards", page, pageSize)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to list cards")
		return
	}

	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, handler.buildCardBase(c.Request.Context(), region, record))
	}

	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	hasNext := page < totalPages

	response.JSON(c, http.StatusOK, gin.H{
		"items": items,
		"pagination": gin.H{
			"page":        page,
			"page_size":   pageSize,
			"total":       total,
			"total_pages": totalPages,
			"has_next":    hasNext,
		},
	})
}

func (handler *CardHandler) buildCardBase(ctx context.Context, region string, record map[string]any) map[string]any {
	if record == nil {
		return map[string]any{}
	}

	keys := []string{
		"id",
		"seq",
		"attr",
		"supportUnit",
		"cardSkillName",
		"prefix",
		"assetbundleName",
		"gachaPhrase",
		"flavorText",
		"releaseAt",
		"archivePublishedAt",
		"initialSpecialTrainingStatus",
	}

	result := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := record[key]; ok {
			result[key] = value
		}
	}

	if handler == nil || handler.masterDataSync == nil {
		return result
	}

	if cardSupplyID, ok := record["cardSupplyId"]; ok {
		lookupID := shared.NormalizeAnyID(cardSupplyID)
		if lookupID == "" {
			result["cardSupply"] = nil
		} else {
			cardSupply, found, err := handler.masterDataSync.GetByID(ctx, region, "cardsupplies", lookupID)
			if err != nil || !found {
				result["cardSupply"] = nil
			} else {
				result["cardSupply"] = cardSupply
			}
		}
	}

	if skillID, ok := record["skillId"]; ok {
		skillLookupID := shared.NormalizeAnyID(skillID)
		if skillLookupID == "" {
			result["skill"] = nil
		} else {
			skill, found, err := handler.masterDataSync.GetByID(ctx, region, "skills", skillLookupID)
			if err != nil || !found {
				result["skill"] = nil
			} else {
				result["skill"] = skill
			}
		}
	}

	if characterID, ok := record["characterId"]; ok {
		characterLookupID := shared.NormalizeAnyID(characterID)
		if characterLookupID == "" {
			result["character"] = nil
		} else {
			character, found, err := handler.masterDataSync.GetByID(ctx, region, "gamecharacters", characterLookupID)
			if err != nil || !found {
				result["character"] = nil
			} else {
				result["character"] = sanitizeGameCharacter(character)
			}
		}
	}

	if cardRarityType, ok := record["cardRarityType"]; ok {
		cardID := shared.NormalizeAnyID(record["id"])
		rarityTypeLookup := shared.NormalizeComparableText(cardRarityType)
		zap.S().Debugw(
			"card rarity lookup start",
			"component", "card-handler",
			"region", region,
			"card_id", cardID,
			"raw_type", cardRarityType,
			"normalized_type", rarityTypeLookup,
		)
		if rarityTypeLookup == "" {
			zap.S().Warnw("card rarity lookup empty type", "component", "card-handler", "region", region, "card_id", cardID)
			result["cardRarity"] = nil
		} else {
			matches, err := handler.masterDataSync.Search(ctx, region, "cardrarities", rarityTypeLookup, []string{"cardRarityType"}, 20)
			if err != nil {
				zap.S().Debugw("card rarity lookup search error", "component", "card-handler", "region", region, "card_id", cardID, "type", rarityTypeLookup, "error", err)
				result["cardRarity"] = nil
			} else {
				zap.S().Debugw("card rarity lookup search done", "component", "card-handler", "region", region, "card_id", cardID, "type", rarityTypeLookup, "matches", len(matches))
				rarity := findExactCardRarityByType(matches, rarityTypeLookup)
				if rarity == nil {
					zap.S().Warnw("card rarity lookup not found", "component", "card-handler", "region", region, "card_id", cardID, "type", rarityTypeLookup)
					result["cardRarity"] = nil
				} else {
					zap.S().Debugw("card rarity lookup found", "component", "card-handler", "region", region, "card_id", cardID, "type", rarityTypeLookup, "rarity_id", rarity["id"])
					result["cardRarity"] = rarity
				}
			}
		}
	} else {
		zap.S().Warnw("card rarity lookup skipped", "component", "card-handler", "reason", "missing_card_rarity_type", "region", region, "card_id", shared.NormalizeAnyID(record["id"]))
	}

	return result
}

func (handler *CardHandler) ensureRegionReady(c *gin.Context, region string) bool {
	if handler == nil || handler.masterDataSync == nil {
		return true
	}

	readyRegions, err := shared.ReadyMasterDataRegions(c.Request.Context(), handler.masterDataSync)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MASTER_DATA_STATUS_ERROR", "failed to check master data sync status")
		return false
	}

	normalizedRegion := strings.ToLower(strings.TrimSpace(region))
	for _, readyRegion := range readyRegions {
		if readyRegion == normalizedRegion {
			return true
		}
	}

	response.Error(c, http.StatusServiceUnavailable, "REGION_DATA_NOT_READY", "region data is updating or unavailable, please try again later")
	return false
}

func sanitizeGameCharacter(character map[string]any) map[string]any {
	if character == nil {
		return map[string]any{}
	}

	result := make(map[string]any, len(character))
	for key, value := range character {
		switch key {
		case "live2dHeightAdjustment", "figure", "breastSize", "modelName":
			continue
		default:
			result[key] = value
		}
	}

	return result
}

func findExactCardRarityByType(matches []masterdata.SearchMatch, rarityType string) map[string]any {
	if rarityType == "" || len(matches) == 0 {
		return nil
	}

	for _, match := range matches {
		candidateType := shared.NormalizeComparableText(match.Item["cardRarityType"])
		if candidateType == rarityType {
			return match.Item
		}
	}

	return nil
}

func buildCardParams(record map[string]any) map[string]any {
	if record == nil {
		return map[string]any{}
	}

	result := map[string]any{}
	for _, key := range []string{
		"id",
		"specialTrainingPower1BonusFixed",
		"specialTrainingPower2BonusFixed",
		"specialTrainingPower3BonusFixed",
		"cardParameters",
	} {
		if value, ok := record[key]; ok {
			result[key] = value
		}
	}

	return result
}
