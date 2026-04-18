package virtuallives

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type VirtualLiveHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
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
// @Success 200 {object} map[string]interface{}
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
	if !handler.ensureRegionReady(c, region) {
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

	response.JSON(c, http.StatusOK, handler.buildVirtualLive(c.Request.Context(), region, record))
}

// ItemsByID godoc
// @Summary Get virtual live items by id
// @Tags virtualLives
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Virtual Live ID"
// @Success 200 {object} map[string]interface{}
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
// @Success 200 {object} map[string]interface{}
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
// @Success 200 {object} map[string]interface{}
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
// @Success 200 {object} map[string]interface{}
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
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} map[string]interface{}
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
		records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, "virtuallives")
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "VIRTUAL_LIVE_QUERY_ERROR", "failed to list virtual lives")
			return
		}
		if !shared.ValidateSortField(c, sortOptions.Field, records, sortableVirtualLiveFields) {
			return
		}
		shared.SortResponseItems(records, sortOptions.Field, sortOptions.Descending)
		pagedRecords, pagination := shared.PaginateItems(records, page, pageSize)
		response.JSON(c, http.StatusOK, gin.H{
			"items":      handler.buildVirtualLiveList(c.Request.Context(), region, pagedRecords),
			"pagination": pagination,
		})
		return
	}

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
}

// Search godoc
// @Summary Search virtual lives
// @Tags virtualLives
// @Produce json
// @Param region path string true "Region"
// @Param q query string true "Search query"
// @Param field query string false "Search field (name|type|assetbundle), default=name"
// @Param page query int false "Page number"
// @Param limit query int false "Max results"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /virtualLives/{region}/search [get]
func (handler *VirtualLiveHandler) Search(c *gin.Context) {
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
	case "type":
		searchFields = []string{"virtualLiveType"}
	case "assetbundle":
		searchFields = []string{"assetbundleName"}
	default:
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "field must be one of: name, type, assetbundle")
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

	matches, err := handler.masterDataSync.Search(c.Request.Context(), region, "virtuallives", query, searchFields, 1000000)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "VIRTUAL_LIVE_QUERY_ERROR", "failed to search virtual lives")
		return
	}

	if sortOptions.Enabled {
		records := make([]map[string]any, 0, len(matches))
		for _, match := range matches {
			records = append(records, match.Item)
		}
		if !shared.ValidateSortField(c, sortOptions.Field, records, sortableVirtualLiveFields) {
			return
		}
		shared.SortResponseItems(records, sortOptions.Field, sortOptions.Descending)
		pagedRecords, pagination := shared.PaginateItems(records, page, limit)
		response.JSON(c, http.StatusOK, gin.H{
			"items":      handler.buildVirtualLiveList(c.Request.Context(), region, pagedRecords),
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
		items = append(items, handler.buildVirtualLive(c.Request.Context(), region, match.Item))
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

func (handler *VirtualLiveHandler) ensureRegionReady(c *gin.Context, region string) bool {
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

func (handler *VirtualLiveHandler) buildVirtualLiveList(ctx context.Context, region string, records []map[string]any) []map[string]any {
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, handler.buildVirtualLive(ctx, region, record))
	}

	return items
}

func (handler *VirtualLiveHandler) buildVirtualLive(ctx context.Context, region string, record map[string]any) map[string]any {
	if record == nil {
		return map[string]any{}
	}

	result := make(map[string]any, len(record)+3)
	for key, value := range record {
		if key == "virtualItems" || key == "virtualLiveSchedules" || key == "virtualLiveSetlists" {
			continue
		}
		result[key] = value
	}

	if rawVirtualLiveGroupID, hasVirtualLiveGroupID := record["virtualLiveGroupId"]; hasVirtualLiveGroupID {
		delete(result, "virtualLiveGroupId")

		virtualLiveGroupLookupID := shared.NormalizeAnyID(rawVirtualLiveGroupID)
		if handler == nil || handler.masterDataSync == nil || virtualLiveGroupLookupID == "" {
			result["virtualLiveGroup"] = nil
		} else {
			virtualLiveGroup, found, err := handler.masterDataSync.GetByID(ctx, region, "virtuallivegroups", virtualLiveGroupLookupID)
			if err != nil || !found {
				result["virtualLiveGroup"] = nil
			} else {
				result["virtualLiveGroup"] = virtualLiveGroup
			}
		}
	}
	result["pamphlet"] = findVirtualLivePamphlet(ctx, handler.masterDataSync, region, shared.NormalizeAnyID(record["id"]))
	result["ticket"] = findVirtualLiveTicket(ctx, handler.masterDataSync, region, shared.NormalizeAnyID(record["id"]))

	return result
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
	if !handler.ensureRegionReady(c, region) {
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
