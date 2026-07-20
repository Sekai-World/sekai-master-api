package virtuallives

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

type VirtualLiveHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
}

type virtualLiveRelatedData struct {
	preloaded bool
	groups    map[string]map[string]any
	vocals    map[string]map[string]any
	pamphlets map[string]map[string]any
	tickets   map[string]map[string]any
}

var sortableVirtualLiveFields = []string{
	"id",
	"name",
	"assetbundleName",
	"startAt",
	"endAt",
	"virtualLiveType",
}

func NewVirtualLiveHandler(masterDataSync *usecase.MasterDataSyncUsecase) *VirtualLiveHandler {
	return &VirtualLiveHandler{masterDataSync: masterDataSync}
}

// ByID godoc
// @Summary Get virtual live by id
// @Tags virtualLives
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Virtual Live ID"
// @Success 200 {object} shared.VirtualLiveObjectResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /virtualLives/{region}/{id} [get]
func (handler *VirtualLiveHandler) ByID(c *gin.Context) {
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, "virtuallives") {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "virtuallives", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "VIRTUAL_LIVE_QUERY_ERROR", "failed to query virtual live")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "VIRTUAL_LIVE_NOT_FOUND", "virtual live not found")
		return
	}

	response.JSON(c, http.StatusOK, handler.buildVirtualLiveObject(c.Request.Context(), region, record))
}

// ItemsByID godoc
// @Summary Get virtual live items by id
// @Tags virtualLives
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Virtual Live ID"
// @Success 200 {object} shared.VirtualLiveItemsResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /virtualLives/{region}/{id}/items [get]
func (handler *VirtualLiveHandler) ItemsByID(c *gin.Context) {
	handler.respondVirtualLiveArrayField(c, "virtualItems", "virtual live items")
}

// SchedulesByID godoc
// @Summary Get virtual live schedules by id
// @Tags virtualLives
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Virtual Live ID"
// @Success 200 {object} shared.VirtualLiveSchedulesResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /virtualLives/{region}/{id}/schedules [get]
func (handler *VirtualLiveHandler) SchedulesByID(c *gin.Context) {
	handler.respondVirtualLiveArrayField(c, "virtualLiveSchedules", "virtual live schedules")
}

// SetlistsByID godoc
// @Summary Get virtual live setlists by id
// @Tags virtualLives
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Virtual Live ID"
// @Success 200 {object} shared.VirtualLiveSetlistsResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /virtualLives/{region}/{id}/setlists [get]
func (handler *VirtualLiveHandler) SetlistsByID(c *gin.Context) {
	handler.respondVirtualLiveArrayField(c, "virtualLiveSetlists", "virtual live setlists")
}

// AvailableRegionsByID godoc
// @Summary Get available regions for a virtual live id
// @Tags virtualLives
// @Produce json
// @Param id path string true "Virtual Live ID"
// @Success 200 {object} shared.RegionAvailabilityResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /virtualLives/regions/{id}/availability [get]
func (handler *VirtualLiveHandler) AvailableRegionsByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "id is required")
		return
	}

	regions, err := shared.AvailableRegionsByID(c.Request.Context(), handler.masterDataSync, "virtuallives", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "VIRTUAL_LIVE_QUERY_ERROR", "failed to query virtual live available regions")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{"regions": regions})
}

// List godoc
// @Summary List virtual lives by page
// @Tags virtualLives
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number"
// @Param page_size query int false "Page size"
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Param name query string false "Case-insensitive (trimmed) substring match against the virtual live name"
// @Param id query int false "Exact virtual live id"
// @Param virtual_live_type query string false "Comma-separated virtual live types (OR within parameter, AND combined with other filters)"
// @Success 200 {object} shared.VirtualLiveListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /virtualLives/{region}/list [get]
func (handler *VirtualLiveHandler) List(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	if region == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region is required")
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, "virtuallives") {
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

	includeSpoilers, ok := shared.ParseSpoilerOption(c)
	if !ok {
		return
	}

	filters, filterOK := parseVirtualLiveListFilters(c)
	if !filterOK {
		return
	}

	hasNewFilter := filters.Name != "" || filters.HasID || len(filters.VirtualLiveTypes) > 0
	usesIndexPage := !hasNewFilter && includeSpoilers && !sortOptions.Enabled

	if usesIndexPage {
		records, total, err := handler.masterDataSync.ListByPage(c.Request.Context(), region, "virtuallives", page, pageSize)
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "VIRTUAL_LIVE_QUERY_ERROR", "failed to list virtual lives")
			return
		}

		totalPages := 0
		if pageSize > 0 {
			totalPages = (total + pageSize - 1) / pageSize
		}

		response.JSON(c, http.StatusOK, gin.H{
			"items": handler.buildVirtualLiveList(c.Request.Context(), region, records),
			"pagination": gin.H{
				"page":        page,
				"page_size":   pageSize,
				"total":       total,
				"total_pages": totalPages,
				"has_next":    page < totalPages,
			},
		})
		return
	}

	records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, "virtuallives")
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "VIRTUAL_LIVE_QUERY_ERROR", "failed to list virtual lives")
		return
	}

	if !includeSpoilers {
		records = shared.FilterSpoilerItems(records, time.Now().UTC())
	}

	records = applyVirtualLiveListFilters(records, filters)

	if sortOptions.Enabled {
		if !shared.ValidateSortField(c, sortOptions.Field, records, sortableVirtualLiveFields) {
			return
		}
		shared.SortResponseItems(records, sortOptions.Field, sortOptions.Descending)
	}

	pagedRecords, pagination := shared.PaginateItems(records, page, pageSize)
	response.JSON(c, http.StatusOK, gin.H{
		"items":      handler.buildVirtualLiveList(c.Request.Context(), region, pagedRecords),
		"pagination": pagination,
	})
}

type virtualLiveListFilters struct {
	Name             string
	ID               int64
	HasID            bool
	VirtualLiveTypes []string
}

// normalizeIDValue parses a record id field into a normalized int64.
// Returns false if the value is not a numeric id (e.g. missing or malformed).
func normalizeIDValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case float64:
		return int64(typed), true
	case float32:
		return int64(typed), true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		if parsed, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return parsed, true
		}
		return 0, false
	default:
		return 0, false
	}
}

func parseVirtualLiveListFilters(c *gin.Context) (virtualLiveListFilters, bool) {
	filters := virtualLiveListFilters{}

	if rawName := strings.TrimSpace(c.Query("name")); rawName != "" {
		filters.Name = shared.NormalizeComparableText(rawName)
	}

	if rawID := strings.TrimSpace(c.Query("id")); rawID != "" {
		parsedID, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "id must be a numeric virtual live id")
			return filters, false
		}
		filters.ID = parsedID
		filters.HasID = true
	}

	if rawTypes := strings.TrimSpace(c.Query("virtual_live_type")); rawTypes != "" {
		seen := map[string]struct{}{}
		for _, part := range strings.Split(rawTypes, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			filters.VirtualLiveTypes = append(filters.VirtualLiveTypes, trimmed)
		}
	}

	return filters, true
}

func applyVirtualLiveListFilters(records []map[string]any, filters virtualLiveListFilters) []map[string]any {
	if len(records) == 0 {
		return records
	}
	if filters.Name == "" && !filters.HasID && len(filters.VirtualLiveTypes) == 0 {
		return records
	}

	matched := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if record == nil {
			continue
		}
		if filters.Name != "" {
			name := shared.NormalizeComparableText(record["name"])
			if !strings.Contains(name, filters.Name) {
				continue
			}
		}
		if filters.HasID {
			recordID, ok := normalizeIDValue(record["id"])
			if !ok || recordID != filters.ID {
				continue
			}
		}
		if len(filters.VirtualLiveTypes) > 0 {
			recordType := shared.NormalizeAnyID(record["virtualLiveType"])
			found := false
			for _, want := range filters.VirtualLiveTypes {
				if recordType == want {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		matched = append(matched, record)
	}

	return matched
}

func (handler *VirtualLiveHandler) buildVirtualLiveList(ctx context.Context, region string, records []map[string]any) []map[string]any {
	related := handler.preloadVirtualLiveRelatedData(ctx, region, records)
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, handler.buildVirtualLiveWithRelated(ctx, region, record, related))
	}

	return items
}

func (handler *VirtualLiveHandler) buildVirtualLiveObject(ctx context.Context, region string, record map[string]any) map[string]any {
	return handler.buildVirtualLiveWithRelated(ctx, region, record, virtualLiveRelatedData{})
}

func (handler *VirtualLiveHandler) buildVirtualLiveWithRelated(
	ctx context.Context,
	region string,
	record map[string]any,
	related virtualLiveRelatedData,
) map[string]any {
	if record == nil {
		return map[string]any{}
	}

	result := make(map[string]any, len(record)+4)
	for key, value := range record {
		if key == "virtualItems" || key == "virtualLiveSchedules" || key == "virtualLiveSetlists" {
			continue
		}
		result[key] = value
	}

	if rawRewards, hasRewards := record["virtualLiveRewards"]; hasRewards {
		resourceBoxes := handler.loadResourceBoxes(ctx, region)
		result["virtualLiveRewards"] = handler.buildVirtualLiveRewards(ctx, region, rawRewards, resourceBoxes)
	}

	if rawVirtualLiveGroupID, hasVirtualLiveGroupID := record["virtualLiveGroupId"]; hasVirtualLiveGroupID {
		delete(result, "virtualLiveGroupId")

		virtualLiveGroupLookupID := shared.NormalizeAnyID(rawVirtualLiveGroupID)
		if virtualLiveGroupLookupID == "" {
			result["virtualLiveGroup"] = nil
		} else if related.preloaded {
			result["virtualLiveGroup"] = related.groups[virtualLiveGroupLookupID]
		} else {
			result["virtualLiveGroup"] = handler.loadVirtualLiveGroup(ctx, region, virtualLiveGroupLookupID)
		}
	}
	if rawScreenMvMusicVocalID, hasScreenMvMusicVocalID := record["screenMvMusicVocalId"]; hasScreenMvMusicVocalID {
		delete(result, "screenMvMusicVocalId")

		screenMvMusicVocalLookupID := shared.NormalizeAnyID(rawScreenMvMusicVocalID)
		if screenMvMusicVocalLookupID == "" {
			result["screenMvMusicVocal"] = nil
		} else if related.preloaded {
			result["screenMvMusicVocal"] = related.vocals[screenMvMusicVocalLookupID]
		} else {
			result["screenMvMusicVocal"] = handler.loadScreenMvMusicVocal(ctx, region, screenMvMusicVocalLookupID)
		}
	}
	virtualLiveID := shared.NormalizeAnyID(record["id"])
	if related.preloaded {
		result["pamphlet"] = related.pamphlets[virtualLiveID]
		result["ticket"] = related.tickets[virtualLiveID]
	} else {
		result["pamphlet"] = findVirtualLivePamphlet(ctx, handler.masterDataSync, region, virtualLiveID)
		result["ticket"] = findVirtualLiveTicket(ctx, handler.masterDataSync, region, virtualLiveID)
	}

	return result
}

func (handler *VirtualLiveHandler) buildVirtualLiveRewards(ctx context.Context, region string, rawRewards any, resourceBoxes []map[string]any) []map[string]any {
	items, ok := rawRewards.([]any)
	if !ok {
		return []map[string]any{}
	}

	rewards := make([]map[string]any, 0, len(items))
	for _, item := range items {
		rewardRecord, ok := item.(map[string]any)
		if !ok {
			continue
		}

		reward := make(map[string]any, len(rewardRecord)+1)
		for key, value := range rewardRecord {
			reward[key] = value
		}
		if resourceBox := handler.resolveVirtualLiveRewardResourceBox(ctx, region, rewardRecord, resourceBoxes); resourceBox != nil {
			reward["resourceBox"] = resourceBox
		}
		rewards = append(rewards, reward)
	}

	return rewards
}

// loadResourceBoxes returns the region's full resourceboxes list. resourceboxes
// IDs are not unique across resourceBoxPurpose, so callers must select by both
// id and purpose rather than relying on GetByID.
func (handler *VirtualLiveHandler) loadResourceBoxes(ctx context.Context, region string) []map[string]any {
	if handler == nil || handler.masterDataSync == nil {
		return nil
	}

	boxes, err := handler.masterDataSync.ListAll(ctx, region, "resourceboxes")
	if err != nil {
		return nil
	}
	return boxes
}

func (handler *VirtualLiveHandler) preloadVirtualLiveRelatedData(
	ctx context.Context,
	region string,
	records []map[string]any,
) virtualLiveRelatedData {
	related := virtualLiveRelatedData{
		preloaded: true,
		groups:    map[string]map[string]any{},
		vocals:    map[string]map[string]any{},
		pamphlets: map[string]map[string]any{},
		tickets:   map[string]map[string]any{},
	}
	if handler == nil || handler.masterDataSync == nil || len(records) == 0 {
		return related
	}

	virtualLiveIDs := map[string]struct{}{}
	groupIDs := map[string]struct{}{}
	vocalIDs := map[string]struct{}{}
	for _, record := range records {
		virtualLiveID := shared.NormalizeAnyID(record["id"])
		if virtualLiveID != "" {
			virtualLiveIDs[virtualLiveID] = struct{}{}
		}
		if groupID := shared.NormalizeAnyID(record["virtualLiveGroupId"]); groupID != "" {
			groupIDs[groupID] = struct{}{}
		}
		if vocalID := shared.NormalizeAnyID(record["screenMvMusicVocalId"]); vocalID != "" {
			vocalIDs[vocalID] = struct{}{}
		}
	}

	for groupID := range groupIDs {
		if group := handler.loadVirtualLiveGroup(ctx, region, groupID); group != nil {
			related.groups[groupID] = group
		}
	}
	for vocalID := range vocalIDs {
		if vocal := handler.loadScreenMvMusicVocal(ctx, region, vocalID); vocal != nil {
			related.vocals[vocalID] = vocal
		}
	}
	for virtualLiveID, pamphlet := range handler.loadVirtualLiveRelatedEntityMap(ctx, region, "virtuallivepamphlets", virtualLiveIDs) {
		related.pamphlets[virtualLiveID] = pamphlet
	}
	for virtualLiveID, ticket := range handler.loadVirtualLiveRelatedEntityMap(ctx, region, "virtuallivetickets", virtualLiveIDs) {
		related.tickets[virtualLiveID] = ticket
	}

	return related
}

func (handler *VirtualLiveHandler) loadVirtualLiveGroup(ctx context.Context, region string, groupID string) map[string]any {
	if handler == nil || handler.masterDataSync == nil || groupID == "" {
		return nil
	}

	virtualLiveGroup, found, err := handler.masterDataSync.GetByID(ctx, region, "virtuallivegroups", groupID)
	if err != nil || !found {
		return nil
	}

	return virtualLiveGroup
}

func (handler *VirtualLiveHandler) loadScreenMvMusicVocal(ctx context.Context, region string, vocalID string) map[string]any {
	if handler == nil || handler.masterDataSync == nil || vocalID == "" {
		return nil
	}

	screenMvMusicVocal, found, err := handler.masterDataSync.GetByID(ctx, region, "musicvocals", vocalID)
	if err != nil || !found {
		return nil
	}

	return screenMvMusicVocal
}

func (handler *VirtualLiveHandler) loadVirtualLiveRelatedEntityMap(
	ctx context.Context,
	region string,
	entity string,
	virtualLiveIDs map[string]struct{},
) map[string]map[string]any {
	items := map[string]map[string]any{}
	if handler == nil || handler.masterDataSync == nil || len(virtualLiveIDs) == 0 {
		return items
	}

	records, err := handler.masterDataSync.ListAll(ctx, region, entity)
	if err != nil {
		return items
	}

	for _, record := range records {
		virtualLiveID := shared.NormalizeAnyID(record["virtualLiveId"])
		if _, ok := virtualLiveIDs[virtualLiveID]; !ok {
			continue
		}

		item := make(map[string]any, len(record))
		for key, value := range record {
			if key == "virtualLiveId" {
				continue
			}
			item[key] = value
		}
		items[virtualLiveID] = item
	}

	return items
}

func (handler *VirtualLiveHandler) respondVirtualLiveArrayField(c *gin.Context, field string, description string) {
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, "virtuallives") {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "virtuallives", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "VIRTUAL_LIVE_QUERY_ERROR", "failed to query "+description)
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "VIRTUAL_LIVE_NOT_FOUND", "virtual live not found")
		return
	}

	items := []any{}
	if value, ok := record[field]; ok {
		switch typed := value.(type) {
		case []any:
			items = typed
		default:
			items = []any{typed}
		}
	}

	response.JSON(c, http.StatusOK, gin.H{"items": items})
}
