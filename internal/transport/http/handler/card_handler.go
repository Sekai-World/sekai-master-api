package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type CardHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
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
// @Success 200 {object} CardObjectResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
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

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "cards", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "CARD_NOT_FOUND", "card not found")
		return
	}

	response.JSON(c, http.StatusOK, buildCardBase(record))
}

// ParamsByID godoc
// @Summary Get card params by id
// @Tags cards
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Card ID"
// @Success 200 {object} CardObjectResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
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

// SearchByPrefix godoc
// @Summary Search cards by prefix
// @Tags cards
// @Produce json
// @Param region path string true "Region"
// @Param q query string true "Prefix query"
// @Param page query int false "Page number"
// @Param limit query int false "Max results"
// @Success 200 {object} CardListResponse
// @Failure 400 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
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

	fetchLimit := 1000000

	matches, err := handler.masterDataSync.Search(c.Request.Context(), region, "cards", query, []string{"prefix"}, fetchLimit)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to search cards")
		return
	}

	total := len(matches)
	start := (page - 1) * limit
	if start >= total {
		totalPages := 0
		if limit > 0 {
			totalPages = (total + limit - 1) / limit
		}
		response.JSON(c, http.StatusOK, gin.H{
			"items": []map[string]any{},
			"pagination": gin.H{
				"page":        page,
				"page_size":   limit,
				"total":       total,
				"total_pages": totalPages,
				"has_next":    false,
			},
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
		items = append(items, buildCardBase(match.Item))
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
// @Success 200 {object} CardListResponse
// @Failure 400 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
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

	records, total, err := handler.masterDataSync.ListByPage(c.Request.Context(), region, "cards", page, pageSize)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to list cards")
		return
	}

	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, buildCardBase(record))
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

func buildCardBase(record map[string]any) map[string]any {
	if record == nil {
		return map[string]any{}
	}

	keys := []string{
		"id",
		"seq",
		"characterId",
		"cardRarityType",
		"attr",
		"supportUnit",
		"skillId",
		"cardSkillName",
		"prefix",
		"assetbundleName",
		"gachaPhrase",
		"flavorText",
		"releaseAt",
		"archivePublishedAt",
		"cardSupplyId",
		"initialSpecialTrainingStatus",
	}

	result := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := record[key]; ok {
			result[key] = value
		}
	}

	return result
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
