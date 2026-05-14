package events

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type EventHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
}

var sortableEventFields = []string{
	"id",
	"startAt",
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
// @Success 200 {object} shared.EventObjectResponse
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
// @Success 200 {object} shared.RegionAvailabilityResponse
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
// @Success 200 {object} shared.CurrentEventResponse
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

	response.JSON(c, http.StatusOK, buildCurrentEventBase(record))
}

// BreakTimesByID godoc
// @Summary Get event break times by event id
// @Tags events
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Event ID"
// @Success 200 {object} shared.GenericObjectResponse
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
// @Param id query string false "Event ID"
// @Param name query string false "Event name"
// @Param unit query string false "Event unit (matched against eventStoryUnits.unit)"
// @Param event_type query string false "Event type"
// @Param sort_by query string false "Sort field (id|startAt)"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.EventListResponse
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

	filterOptions := parseEventFilterOptions(c)
	if filterOptions.Enabled || sortOptions.Enabled {
		records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, "events")
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "EVENT_QUERY_ERROR", "failed to list events")
			return
		}
		records = handler.filterEvents(c.Request.Context(), region, records, filterOptions)
		if sortOptions.Enabled {
			if !shared.ValidateSortField(c, sortOptions.Field, records, sortableEventFields) {
				return
			}
			shared.SortResponseItems(records, sortOptions.Field, sortOptions.Descending)
		}
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

// RewardsByID godoc
// @Summary Get event rewards by id
// @Tags events
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Event ID"
// @Success 200 {object} shared.EventRewardsResponse
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
// @Success 200 {object} shared.EventMusicsResponse
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
// @Success 200 {object} shared.EventCardsResponse
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
// @Success 200 {object} shared.EventBonusesResponse
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

func buildCurrentEventBase(record map[string]any) map[string]any {
	return pickFields(record, []string{
		"id",
		"name",
		"startAt",
		"aggregateAt",
		"assetbundleName",
		"closedAt",
		"eventType",
		"unit",
	})
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

	eventStory := handler.findEventStoryByEventID(ctx, region, shared.NormalizeAnyID(record["id"]))

	if unit := handler.resolveEventUnit(ctx, region, record, eventStory); unit != nil {
		result["unit"] = unit
	}

	if bannerGameCharacter := handler.resolveBannerGameCharacter(ctx, region, eventStory); bannerGameCharacter != nil {
		result["bannerGameCharacter"] = bannerGameCharacter
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

func (handler *EventHandler) findEventStoryByEventID(ctx context.Context, region string, eventID string) map[string]any {
	if eventID == "" {
		return nil
	}

	matches, err := handler.masterDataSync.Search(ctx, region, "eventstories", eventID, []string{"eventId"}, 10)
	if err != nil {
		return nil
	}

	for _, match := range matches {
		if shared.NormalizeAnyID(match.Item["eventId"]) == eventID {
			return match.Item
		}
	}

	return nil
}

func (handler *EventHandler) resolveEventUnit(ctx context.Context, region string, record map[string]any, eventStory map[string]any) map[string]any {
	unitLookup := ""
	if eventStory != nil {
		eventStoryID := shared.NormalizeAnyID(eventStory["id"])
		if eventStoryID != "" {
			matches, err := handler.masterDataSync.Search(ctx, region, "eventstoryunits", eventStoryID, []string{"eventStoryId"}, 20)
			if err == nil {
				unitLookup = pickPrimaryEventStoryUnit(matches)
			}
		}
	}

	if unitLookup == "" {
		unitLookup = shared.NormalizeComparableText(record["unit"])
	}
	if unitLookup == "" {
		return nil
	}

	matches, err := handler.masterDataSync.Search(ctx, region, "unitprofiles", unitLookup, []string{"unit"}, 1)
	if err != nil || len(matches) == 0 {
		return nil
	}

	return pickFields(matches[0].Item, []string{"unit", "unitName", "colorCode"})
}

func pickPrimaryEventStoryUnit(matches []masterdata.SearchMatch) string {
	var fallback string
	for _, match := range matches {
		unit := shared.NormalizeComparableText(match.Item["unit"])
		if unit == "" {
			continue
		}
		if fallback == "" {
			fallback = unit
		}
		if shared.NormalizeComparableText(match.Item["eventStoryUnitRelation"]) == "main" {
			return unit
		}
	}

	return fallback
}

func (handler *EventHandler) resolveBannerGameCharacter(ctx context.Context, region string, eventStory map[string]any) map[string]any {
	if eventStory == nil {
		return nil
	}

	bannerGameCharacterUnitID := shared.NormalizeAnyID(eventStory["bannerGameCharacterUnitId"])
	if bannerGameCharacterUnitID == "" {
		return nil
	}

	gameCharacterUnit, found, err := handler.masterDataSync.GetByID(ctx, region, "gamecharacterunits", bannerGameCharacterUnitID)
	if err != nil || !found {
		return nil
	}

	result := pickFields(gameCharacterUnit, []string{"gameCharacterId", "unit", "colorCode"})
	result["gameCharacterUnitId"] = gameCharacterUnit["id"]

	gameCharacterID := shared.NormalizeAnyID(gameCharacterUnit["gameCharacterId"])
	if gameCharacterID == "" {
		return result
	}

	gameCharacter, found, err := handler.masterDataSync.GetByID(ctx, region, "gamecharacters", gameCharacterID)
	if err != nil || !found {
		return result
	}

	for key, value := range pickFields(gameCharacter, []string{"firstName", "givenName"}) {
		result[key] = value
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

type eventFilterOptions struct {
	Enabled bool
	Fields  map[string]string
	Unit    string
}

func parseEventFilterOptions(c *gin.Context) eventFilterOptions {
	fields := map[string]string{}
	for _, queryField := range []string{"id", "name", "event_type"} {
		if value := strings.TrimSpace(c.Query(queryField)); value != "" {
			fields[eventQueryFieldToRecordField(queryField)] = value
		}
	}

	unit := strings.TrimSpace(c.Query("unit"))

	return eventFilterOptions{
		Enabled: len(fields) > 0 || unit != "",
		Fields:  fields,
		Unit:    unit,
	}
}

func eventQueryFieldToRecordField(field string) string {
	switch field {
	case "event_type":
		return "eventType"
	default:
		return field
	}
}

func (handler *EventHandler) filterEvents(ctx context.Context, region string, records []map[string]any, options eventFilterOptions) []map[string]any {
	if !options.Enabled {
		return records
	}

	filtered := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if eventMatchesFilters(record, options.Fields) && handler.eventMatchesUnitFilter(ctx, region, record, options.Unit) {
			filtered = append(filtered, record)
		}
	}

	return filtered
}

func (handler *EventHandler) eventMatchesUnitFilter(ctx context.Context, region string, record map[string]any, unitQuery string) bool {
	queryText := shared.NormalizeComparableText(unitQuery)
	if queryText == "" {
		return true
	}

	eventID := shared.NormalizeAnyID(record["id"])
	if eventID == "" {
		return false
	}

	eventStory := handler.findEventStoryByEventID(ctx, region, eventID)
	if eventStory == nil {
		return false
	}

	eventStoryID := shared.NormalizeAnyID(eventStory["id"])
	if eventStoryID == "" {
		return false
	}

	matches, err := handler.masterDataSync.Search(ctx, region, "eventstoryunits", eventStoryID, []string{"eventStoryId"}, 20)
	if err != nil {
		return false
	}

	for _, match := range matches {
		if shared.NormalizeAnyID(match.Item["eventStoryId"]) != eventStoryID {
			continue
		}
		if strings.Contains(shared.NormalizeComparableText(match.Item["unit"]), queryText) {
			return true
		}
	}

	return false
}

func eventMatchesFilters(record map[string]any, fields map[string]string) bool {
	for field, value := range fields {
		if !eventFieldMatches(record[field], value, field == "id") {
			return false
		}
	}

	return true
}

func eventFieldMatches(recordValue any, query string, exact bool) bool {
	recordText := shared.NormalizeComparableText(recordValue)
	queryText := shared.NormalizeComparableText(query)
	if queryText == "" {
		return true
	}
	if exact {
		return recordText == queryText
	}

	return strings.Contains(recordText, queryText)
}
