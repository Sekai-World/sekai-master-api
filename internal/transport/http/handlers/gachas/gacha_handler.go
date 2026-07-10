package gachas

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

type GachaHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
}

var sortableGachaFields = []string{
	"id",
	"startAt",
}

func NewGachaHandler(masterDataSync *usecase.MasterDataSyncUsecase) *GachaHandler {
	return &GachaHandler{masterDataSync: masterDataSync}
}

// ByID godoc
// @Summary Get gacha basic info by id
// @Tags gachas
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Gacha ID"
// @Success 200 {object} shared.GachaObjectResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /gachas/{region}/{id} [get]
func (handler *GachaHandler) ByID(c *gin.Context) {
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
	if !handler.ensureRegionReadyForEntityRecords(c, region, "gachas") {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "gachas", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "GACHA_QUERY_ERROR", "failed to query gacha")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "GACHA_NOT_FOUND", "gacha not found")
		return
	}

	response.JSON(c, http.StatusOK, handler.buildGachaDetail(c.Request.Context(), region, record))
}

// AvailableRegionsByID godoc
// @Summary Get available regions for a gacha id
// @Tags gachas
// @Produce json
// @Param id path string true "Gacha ID"
// @Success 200 {object} shared.RegionAvailabilityResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /gachas/regions/{id}/availability [get]
func (handler *GachaHandler) AvailableRegionsByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "id is required")
		return
	}

	regions, err := shared.AvailableRegionsByID(c.Request.Context(), handler.masterDataSync, "gachas", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "GACHA_QUERY_ERROR", "failed to query gacha available regions")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{"regions": regions})
}

// List godoc
// @Summary List gachas by page
// @Tags gachas
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number"
// @Param page_size query int false "Page size"
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field (id|startAt)"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.GachaListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /gachas/{region}/list [get]
func (handler *GachaHandler) List(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	if region == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region is required")
		return
	}
	if !handler.ensureRegionReadyForEntityRecords(c, region, "gachas") {
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

	if !includeSpoilers || sortOptions.Enabled {
		records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, "gachas")
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "GACHA_QUERY_ERROR", "failed to list gachas")
			return
		}
		if !includeSpoilers {
			records = shared.FilterSpoilerItems(records, time.Now().UTC())
		}
		if sortOptions.Enabled {
			if !shared.ValidateSortField(c, sortOptions.Field, records, sortableGachaFields) {
				return
			}
			shared.SortResponseItems(records, sortOptions.Field, sortOptions.Descending)
		}
		pagedRecords, pagination := shared.PaginateItems(records, page, pageSize)
		response.JSON(c, http.StatusOK, gin.H{
			"items":      handler.buildGachaList(c.Request.Context(), region, pagedRecords),
			"pagination": pagination,
		})
		return
	}

	records, total, err := handler.masterDataSync.ListByPage(c.Request.Context(), region, "gachas", page, pageSize)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "GACHA_QUERY_ERROR", "failed to list gachas")
		return
	}

	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}

	response.JSON(c, http.StatusOK, gin.H{
		"items": handler.buildGachaList(c.Request.Context(), region, records),
		"pagination": gin.H{
			"page":        page,
			"page_size":   pageSize,
			"total":       total,
			"total_pages": totalPages,
			"has_next":    page < totalPages,
		},
	})
}

func (handler *GachaHandler) buildGachaList(ctx context.Context, region string, records []map[string]any) []map[string]any {
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, handler.buildGachaListItem(region, record))
	}
	return items
}

func (handler *GachaHandler) buildGachaListItem(region string, record map[string]any) map[string]any {
	return pickFields(record, []string{
		"id",
		"gachaType",
		"name",
		"assetbundleName",
		"startAt",
		"endAt",
	})
}

func (handler *GachaHandler) buildGachaDetail(ctx context.Context, region string, record map[string]any) map[string]any {
	result := pickFields(record, []string{
		"id",
		"gachaType",
		"name",
		"assetbundleName",
		"summary",
		"startAt",
		"endAt",
		"costResourceType",
		"costResourceId",
		"costCount",
		"gachaCeilItemId",
		"wishFixedSelectCount",
		"wishLimitedSelectCount",
		"wishSelectCount",
		"isShowPeriod",
	})

	if pickupsRaw, ok := record["gachaPickups"]; ok {
		if pickups, ok := pickupsRaw.([]any); ok {
			items := make([]map[string]any, 0, len(pickups))
			for _, pickupRaw := range pickups {
				if pickup, ok := pickupRaw.(map[string]any); ok {
					items = append(items, pickFields(pickup, []string{"cardId", "weight"}))
				}
			}
			result["gachaPickups"] = items
		}
	}

	if ratesRaw, ok := record["gachaCardRarityRates"]; ok {
		if rates, ok := ratesRaw.([]any); ok {
			items := make([]map[string]any, 0, len(rates))
			for _, rateRaw := range rates {
				if rate, ok := rateRaw.(map[string]any); ok {
					items = append(items, pickFields(rate, []string{"id", "cardRarityType", "rate", "lotteryType"}))
				}
			}
			result["gachaCardRarityRates"] = items
		}
	}

	if behaviorsRaw, ok := record["gachaBehaviors"]; ok {
		if behaviors, ok := behaviorsRaw.([]any); ok {
			items := make([]map[string]any, 0, len(behaviors))
			for _, behaviorRaw := range behaviors {
				if behavior, ok := behaviorRaw.(map[string]any); ok {
					items = append(items, pickFields(behavior, []string{
						"id", "gachaBehaviorType", "gachaSpinnableType",
						"costResourceType", "costResourceQuantity", "costResourceId",
						"resourceCategory", "spinCount", "executeLimit",
						"priority", "groupId",
					}))
				}
			}
			result["gachaBehaviors"] = items
		}
	}

	if detailsRaw, ok := record["gachaDetails"]; ok {
		if details, ok := detailsRaw.([]any); ok {
			items := make([]map[string]any, 0, len(details))
			for _, detailRaw := range details {
				if detail, ok := detailRaw.(map[string]any); ok {
					items = append(items, pickFields(detail, []string{"id", "gachaId", "cardId", "weight", "isWish"}))
				}
			}
			result["gachaDetails"] = items
		}
	}

	if infoRaw, ok := record["gachaInformation"]; ok {
		if info, ok := infoRaw.(map[string]any); ok {
			result["gachaInformation"] = pickFields(info, []string{"summary", "description"})
		}
	}

	return result
}

func (handler *GachaHandler) ensureRegionReady(c *gin.Context, region string) bool {
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

	response.Error(c, http.StatusServiceUnavailable, "REGION_NOT_READY", "master data for this region is not available")
	return false
}

func (handler *GachaHandler) ensureRegionReadyForEntityRecords(c *gin.Context, region string, entity string) bool {
	if handler == nil || handler.masterDataSync == nil {
		return true
	}

	ready, err := shared.RegionHasEntityRecordsOrReady(c.Request.Context(), handler.masterDataSync, region, entity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MASTER_DATA_STATUS_ERROR", "failed to check master data sync status")
		return false
	}
	if ready {
		return true
	}

	response.Error(c, http.StatusServiceUnavailable, "REGION_NOT_READY", "master data for this region is not available")
	return false
}

func pickFields(record map[string]any, fields []string) map[string]any {
	result := make(map[string]any, len(fields))
	for _, field := range fields {
		if value, ok := record[field]; ok {
			result[field] = value
		}
	}
	return result
}
