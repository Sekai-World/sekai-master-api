package events

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
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
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
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
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
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

	regions, err := shared.AvailableRegionsByID(c.Request.Context(), handler.masterDataSync, "events", id)
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
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
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

// BreakTimesByID godoc
// @Summary Get event break times by event id
// @Tags events
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Event ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /events/{region}/{id}/break-times [get]
func (handler *EventHandler) BreakTimesByID(c *gin.Context) {
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

	breakTimeID := shared.NormalizeAnyID(record["eventBreakTimeId"])
	if breakTimeID == "" {
		response.Error(c, http.StatusNotFound, "EVENT_BREAK_TIME_NOT_FOUND", "event break time not found")
		return
	}

	breakTime, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "eventbreaktimes", breakTimeID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "EVENT_QUERY_ERROR", "failed to query event break times")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "EVENT_BREAK_TIME_NOT_FOUND", "event break time not found")
		return
	}

	response.JSON(c, http.StatusOK, breakTime)
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
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
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

	sortOptions, ok := shared.ParseListSortOptions(c)
	if !ok {
		return
	}

	if sortOptions.Enabled {
		records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, "events")
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "EVENT_QUERY_ERROR", "failed to list events")
			return
		}
		if !shared.ValidateSortField(c, sortOptions.Field, records, sortableEventFields) {
			return
		}
		shared.SortResponseItems(records, sortOptions.Field, sortOptions.Descending)
		pagedRecords, pagination := shared.PaginateItems(records, page, pageSize)
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
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
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

	sortOptions, ok := shared.ParseListSortOptions(c)
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
		if !shared.ValidateSortField(c, sortOptions.Field, records, sortableEventFields) {
			return
		}
		shared.SortResponseItems(records, sortOptions.Field, sortOptions.Descending)
		pagedRecords, pagination := shared.PaginateItems(records, page, limit)
		response.JSON(c, http.StatusOK, gin.H{
			"items":      handler.buildEventList(c.Request.Context(), region, pagedRecords),
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
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
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

// MusicsByID godoc
// @Summary Get event musics by id
// @Tags events
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Event ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /events/{region}/{id}/musics [get]
func (handler *EventHandler) MusicsByID(c *gin.Context) {
	items, ok := handler.loadEventBonusItems(c, "eventmusics", "event musics")
	if !ok {
		return
	}

	response.JSON(c, http.StatusOK, gin.H{"items": items})
}

// CardsByID godoc
// @Summary Get event cards by id
// @Tags events
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Event ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /events/{region}/{id}/cards [get]
func (handler *EventHandler) CardsByID(c *gin.Context) {
	items, ok := handler.loadEventBonusItems(c, "eventcards", "event cards")
	if !ok {
		return
	}

	response.JSON(c, http.StatusOK, gin.H{"items": items})
}

// BonusesByID godoc
// @Summary Get event bonuses by id
// @Tags events
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Event ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /events/{region}/{id}/bonuses [get]
func (handler *EventHandler) BonusesByID(c *gin.Context) {
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
	if ok := handler.ensureEventExists(c, region, id); !ok {
		return
	}

	type bonusDataset struct {
		ResponseKey string
		Entity      string
		Label       string
		ScopedByID  bool
	}

	datasets := []bonusDataset{
		{ResponseKey: "eventCardBonusLimits", Entity: "eventcardbonuslimits", Label: "event card bonus limits", ScopedByID: true},
		{ResponseKey: "eventDeckBonuses", Entity: "eventdeckbonuses", Label: "event deck bonuses", ScopedByID: true},
		{ResponseKey: "eventHonorBonuses", Entity: "eventhonorbonuses", Label: "event honor bonuses", ScopedByID: true},
		{ResponseKey: "eventMysekaiFixtureGameCharacterPerformanceBonusLimits", Entity: "eventmysekaifixturegamecharacterperformancebonuslimits", Label: "event mysekai fixture game character performance bonus limits", ScopedByID: true},
		{ResponseKey: "eventRarityBonusRates", Entity: "eventraritybonusrates", Label: "event rarity bonus rates", ScopedByID: false},
	}

	payload := gin.H{}
	for _, dataset := range datasets {
		var (
			items []map[string]any
			err   error
		)
		if dataset.ScopedByID {
			items, err = handler.findEventBonusItems(c.Request.Context(), region, id, dataset.Entity)
		} else {
			items, err = handler.masterDataSync.ListAll(c.Request.Context(), region, dataset.Entity)
		}
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "EVENT_QUERY_ERROR", "failed to query "+dataset.Label)
			return
		}
		payload[dataset.ResponseKey] = items
	}

	response.JSON(c, http.StatusOK, payload)
}

func (handler *EventHandler) loadEventBonusItems(c *gin.Context, entity string, label string) ([]map[string]any, bool) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return nil, false
	}

	region := strings.TrimSpace(c.Param("region"))
	id := strings.TrimSpace(c.Param("id"))
	if region == "" || id == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region and id are required")
		return nil, false
	}
	if !handler.ensureRegionReady(c, region) {
		return nil, false
	}

	if ok := handler.ensureEventExists(c, region, id); !ok {
		return nil, false
	}

	items, err := handler.findEventBonusItems(c.Request.Context(), region, id, entity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "EVENT_QUERY_ERROR", "failed to query "+label)
		return nil, false
	}

	return items, true
}

func (handler *EventHandler) ensureEventExists(c *gin.Context, region string, id string) bool {
	_, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "events", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "EVENT_QUERY_ERROR", "failed to query event")
		return false
	}
	if !found {
		response.Error(c, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found")
		return false
	}

	return true
}

func (handler *EventHandler) findEventBonusItems(ctx context.Context, region string, eventID string, entity string) ([]map[string]any, error) {
	matches, err := handler.masterDataSync.Search(ctx, region, entity, eventID, []string{"eventId"}, 1000000)
	if err != nil {
		return nil, err
	}

	items := make([]map[string]any, 0, len(matches))
	targetEventID := shared.NormalizeAnyID(eventID)
	for _, match := range matches {
		if shared.NormalizeAnyID(match.Item["eventId"]) != targetEventID {
			continue
		}
		items = append(items, shared.BuildRecordWithReleaseCondition(ctx, handler.masterDataSync, region, match.Item))
	}

	return items, nil
}

func (handler *EventHandler) ensureRegionReady(c *gin.Context, region string) bool {
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

	if eventPointAssetbundleName := shared.NormalizeAnyID(record["eventPointAssetbundleName"]); eventPointAssetbundleName != "" {
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
		unitLookup := shared.NormalizeComparableText(rawUnit)
		if unitLookup != "" {
			if matches, err := handler.masterDataSync.Search(ctx, region, "unitprofiles", unitLookup, []string{"unit"}, 1); err == nil && len(matches) > 0 {
				result["unit"] = pickFields(matches[0].Item, []string{"unit", "unitName", "colorCode"})
			}
		}
	}

	if rawVirtualLiveID, hasVirtualLiveID := record["virtualLiveId"]; hasVirtualLiveID {
		delete(result, "virtualLiveId")

		virtualLiveLookupID := shared.NormalizeAnyID(rawVirtualLiveID)
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
