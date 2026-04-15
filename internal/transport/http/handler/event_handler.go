package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type EventHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
}

var sortableEventFields = []string{
	"id",
	"eventType",
	"name",
	"assetbundleName",
	"bgmAssetbundleName",
	"unit",
	"startAt",
	"aggregateAt",
	"closedAt",
}

func NewEventHandler(masterDataSync *usecase.MasterDataSyncUsecase) *EventHandler {
	return &EventHandler{masterDataSync: masterDataSync}
}

// ByID godoc
// @Summary Get event basic info by id
// @Tags events
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Event ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /events/{region}/{id} [get]
func (handler *EventHandler) ByID(c *gin.Context) {
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

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "events", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "EVENT_QUERY_ERROR", "failed to query event")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found")
		return
	}

	response.JSON(c, http.StatusOK, handler.buildEventDetail(c.Request.Context(), region, record))
}

// AvailableRegionsByID godoc
// @Summary Get available regions for an event id
// @Tags events
// @Produce json
// @Param id path string true "Event ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /events/regions/{id}/availability [get]
func (handler *EventHandler) AvailableRegionsByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "id is required")
		return
	}

	regions, err := availableRegionsByID(c.Request.Context(), handler.masterDataSync, "events", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "EVENT_QUERY_ERROR", "failed to query event available regions")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{"regions": regions})
}

// Current godoc
// @Summary Get current event by region
// @Tags events
// @Produce json
// @Param region path string true "Region"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /events/{region}/current [get]
func (handler *EventHandler) Current(c *gin.Context) {
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

	record, found, err := handler.masterDataSync.CurrentEvent(c.Request.Context(), region, time.Now().UTC())
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "EVENT_QUERY_ERROR", "failed to query current event")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "EVENT_NOT_FOUND", "current event not found")
		return
	}

	response.JSON(c, http.StatusOK, buildEventBase(record))
}

// List godoc
// @Summary List events by page
// @Tags events
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number"
// @Param page_size query int false "Page size"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /events/{region}/list [get]
func (handler *EventHandler) List(c *gin.Context) {
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

	sortOptions, ok := parseListSortOptions(c)
	if !ok {
		return
	}

	if sortOptions.Enabled {
		records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, "events")
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "EVENT_QUERY_ERROR", "failed to list events")
			return
		}
		if !validateSortField(c, sortOptions.Field, records, sortableEventFields) {
			return
		}
		sortResponseItems(records, sortOptions.Field, sortOptions.Descending)
		pagedRecords, pagination := paginateItems(records, page, pageSize)
		response.JSON(c, http.StatusOK, gin.H{
			"items":      handler.buildEventList(c.Request.Context(), region, pagedRecords),
			"pagination": pagination,
		})
		return
	}

	records, total, err := handler.masterDataSync.ListByPage(c.Request.Context(), region, "events", page, pageSize)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "EVENT_QUERY_ERROR", "failed to list events")
		return
	}

	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}

	response.JSON(c, http.StatusOK, gin.H{
		"items": handler.buildEventList(c.Request.Context(), region, records),
		"pagination": gin.H{
			"page":        page,
			"page_size":   pageSize,
			"total":       total,
			"total_pages": totalPages,
			"has_next":    page < totalPages,
		},
	})
}

// Search godoc
// @Summary Search events
// @Tags events
// @Produce json
// @Param region path string true "Region"
// @Param q query string true "Search query"
// @Param field query string false "Search field (name|unit), default=name"
// @Param page query int false "Page number"
// @Param limit query int false "Max results"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /events/{region}/search [get]
func (handler *EventHandler) Search(c *gin.Context) {
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

	searchFields := []string{"name"}
	switch field {
	case "name":
		searchFields = []string{"name"}
	case "unit":
		searchFields = []string{"unit"}
	default:
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "field must be one of: name, unit")
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

	sortOptions, ok := parseListSortOptions(c)
	if !ok {
		return
	}

	matches, err := handler.masterDataSync.Search(c.Request.Context(), region, "events", query, searchFields, 1000000)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "EVENT_QUERY_ERROR", "failed to search events")
		return
	}

	if sortOptions.Enabled {
		records := make([]map[string]any, 0, len(matches))
		for _, match := range matches {
			records = append(records, match.Item)
		}
		if !validateSortField(c, sortOptions.Field, records, sortableEventFields) {
			return
		}
		sortResponseItems(records, sortOptions.Field, sortOptions.Descending)
		pagedRecords, pagination := paginateItems(records, page, limit)
		response.JSON(c, http.StatusOK, gin.H{
			"items":      handler.buildEventList(c.Request.Context(), region, pagedRecords),
			"pagination": pagination,
		})
		return
	}

	total := len(matches)
	start := (page - 1) * limit
	if start >= total {
		_, pagination := paginateItems([]map[string]any{}, page, limit)
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

	items := make([]map[string]any, 0, end-start)
	for _, match := range matches[start:end] {
		items = append(items, handler.buildEventDetail(c.Request.Context(), region, match.Item))
	}

	totalPages := 0
	if limit > 0 {
		totalPages = (total + limit - 1) / limit
	}

	response.JSON(c, http.StatusOK, gin.H{
		"items": items,
		"pagination": gin.H{
			"page":        page,
			"page_size":   limit,
			"total":       total,
			"total_pages": totalPages,
			"has_next":    page < totalPages,
		},
	})
}

// RewardsByID godoc
// @Summary Get event rewards by id
// @Tags events
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Event ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /events/{region}/{id}/rewards [get]
func (handler *EventHandler) RewardsByID(c *gin.Context) {
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

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "events", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "EVENT_QUERY_ERROR", "failed to query event rewards")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found")
		return
	}

	rewards := []any{}
	if value, ok := record["eventRankingRewardRanges"]; ok {
		switch typed := value.(type) {
		case []any:
			rewards = typed
		default:
			rewards = []any{typed}
		}
	}

	response.JSON(c, http.StatusOK, gin.H{"items": rewards})
}

func (handler *EventHandler) ensureRegionReady(c *gin.Context, region string) bool {
	if handler == nil || handler.masterDataSync == nil {
		return true
	}

	readyRegions, err := readyMasterDataRegions(c.Request.Context(), handler.masterDataSync)
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

func buildEventBase(record map[string]any) map[string]any {
	if record == nil {
		return map[string]any{}
	}

	result := make(map[string]any, len(record))
	for key, value := range record {
		if key == "eventRankingRewardRanges" || key == "eventPointAssetbundleName" {
			continue
		}
		result[key] = value
	}

	if eventPointAssetbundleName := normalizeAnyID(record["eventPointAssetbundleName"]); eventPointAssetbundleName != "" {
		result["eventPointIcon"] = "thumbnail/common_event/" + eventPointAssetbundleName + "/icon_eventpoint"
	}

	return result
}

func (handler *EventHandler) buildEventList(ctx context.Context, region string, records []map[string]any) []map[string]any {
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, handler.buildEventDetail(ctx, region, record))
	}

	return items
}

func (handler *EventHandler) buildEventDetail(ctx context.Context, region string, record map[string]any) map[string]any {
	result := buildEventBase(record)
	if record == nil || handler == nil || handler.masterDataSync == nil {
		return result
	}

	if rawUnit, hasUnit := record["unit"]; hasUnit {
		unitLookup := normalizeComparableText(rawUnit)
		if unitLookup != "" {
			if matches, err := handler.masterDataSync.Search(ctx, region, "unitprofiles", unitLookup, []string{"unit"}, 1); err == nil && len(matches) > 0 {
				result["unit"] = pickFields(matches[0].Item, []string{"unit", "unitName", "colorCode"})
			}
		}
	}

	if rawVirtualLiveID, hasVirtualLiveID := record["virtualLiveId"]; hasVirtualLiveID {
		delete(result, "virtualLiveId")

		virtualLiveLookupID := normalizeAnyID(rawVirtualLiveID)
		if virtualLiveLookupID == "" {
			result["virtualLive"] = nil
		} else {
			virtualLive, found, err := handler.masterDataSync.GetByID(ctx, region, "virtuallives", virtualLiveLookupID)
			if err != nil || !found {
				result["virtualLive"] = nil
			} else {
				result["virtualLive"] = pickFields(virtualLive, []string{"assetbundleName", "endAt", "id", "name", "startAt", "virtualLiveType"})
			}
		}
	}

	return result
}

func pickFields(record map[string]any, keys []string) map[string]any {
	if record == nil {
		return map[string]any{}
	}

	result := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := record[key]; ok {
			result[key] = value
		}
	}

	return result
}
