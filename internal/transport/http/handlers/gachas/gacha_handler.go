package gachas

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"sort"
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
	if !ensureGachaRegionReady(c, handler.masterDataSync, region) {
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

	item, err := handler.buildGachaDetail(c.Request.Context(), region, record)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "GACHA_QUERY_ERROR", "failed to enrich gacha")
		return
	}

	response.JSON(c, http.StatusOK, item)
}

// RateChoiceWishesByID godoc
// @Summary Get rate choice wishes for a gacha
// @Tags gachas
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Gacha ID"
// @Success 200 {object} shared.GachaRateChoiceWishesResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /gachas/{region}/{id}/rate-choice-wishes [get]
func (handler *GachaHandler) RateChoiceWishesByID(c *gin.Context) {
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
	if !ensureGachaRegionReady(c, handler.masterDataSync, region) {
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

	gachaID := any(id)
	if persistedGachaID, ok := record["id"]; ok && persistedGachaID != nil {
		gachaID = persistedGachaID
	}

	result := shared.GachaRateChoiceWishesResponse{
		GachaID:                    gachaID,
		RateChoiceGachaWishGroupID: nil,
		Items:                      make([]shared.GachaRateChoiceWishResponse, 0),
	}

	groupID, configured := record["rateChoiceGachaWishGroupId"]
	if !configured || groupID == nil {
		response.JSON(c, http.StatusOK, result)
		return
	}
	result.RateChoiceGachaWishGroupID = groupID

	wishRecords, err := handler.masterDataSync.ListAll(c.Request.Context(), region, "ratechoicegachawishes")
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "GACHA_QUERY_ERROR", "failed to query rate choice wishes")
		return
	}

	for _, wishRecord := range wishRecords {
		if !rateChoiceValuesEqual(wishRecord["groupId"], groupID) {
			continue
		}

		item, ok := buildRateChoiceWishResponse(wishRecord)
		if !ok {
			continue
		}
		result.Items = append(result.Items, item)
	}

	sort.SliceStable(result.Items, func(i, j int) bool {
		seqComparison := compareRateChoiceValues(result.Items[i].Seq, result.Items[j].Seq)
		if seqComparison != 0 {
			return seqComparison < 0
		}
		return compareRateChoiceValues(result.Items[i].ID, result.Items[j].ID) < 0
	})

	response.JSON(c, http.StatusOK, result)
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
	if !ensureGachaRegionReady(c, handler.masterDataSync, region) {
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

func ensureGachaRegionReady(c *gin.Context, masterDataSync *usecase.MasterDataSyncUsecase, region string) bool {
	if masterDataSync == nil {
		return true
	}

	ready, err := shared.RegionHasEntityRecordsOrReady(c.Request.Context(), masterDataSync, region, "gachas")
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

func (handler *GachaHandler) buildGachaDetail(ctx context.Context, region string, record map[string]any) (map[string]any, error) {
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
	ticketLookups := make(map[string]gachaTicketLookup)

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
					item := pickFields(behavior, []string{
						"id", "gachaBehaviorType", "gachaSpinnableType",
						"costResourceType", "costResourceQuantity", "costResourceId",
						"resourceCategory", "spinCount", "executeLimit",
						"priority", "groupId",
					})
					if err := handler.enrichGachaTicketBehavior(ctx, region, behavior, item, ticketLookups); err != nil {
						return nil, err
					}
					items = append(items, item)
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

	return result, nil
}

type gachaTicketLookup struct {
	record map[string]any
	found  bool
}

func (handler *GachaHandler) enrichGachaTicketBehavior(
	ctx context.Context,
	region string,
	behavior map[string]any,
	result map[string]any,
	lookups map[string]gachaTicketLookup,
) error {
	if handler == nil || handler.masterDataSync == nil || shared.NormalizeComparableText(behavior["costResourceType"]) != "gacha_ticket" {
		return nil
	}

	ticketID := normalizeGachaTicketID(behavior["costResourceId"])
	if ticketID == "" {
		return nil
	}

	lookup, cached := lookups[ticketID]
	if !cached {
		ticket, found, err := handler.masterDataSync.GetByID(ctx, region, "gachatickets", ticketID)
		if err != nil {
			return fmt.Errorf("get gacha ticket %s: %w", ticketID, err)
		}
		lookup = gachaTicketLookup{record: ticket, found: found}
		lookups[ticketID] = lookup
	}
	if !lookup.found || lookup.record == nil {
		return nil
	}

	assetbundleName, ok := lookup.record["assetbundleName"].(string)
	assetbundleName = strings.TrimSpace(assetbundleName)
	if ok && assetbundleName != "" {
		result["costResourceAssetbundleName"] = assetbundleName
	}
	return nil
}

func normalizeGachaTicketID(value any) string {
	switch typed := value.(type) {
	case string:
		return normalizeGachaTicketIntegerText(typed)
	case json.Number:
		return normalizeGachaTicketIntegerText(typed.String())
	case int:
		return normalizeGachaTicketSignedInteger(int64(typed))
	case int8:
		return normalizeGachaTicketSignedInteger(int64(typed))
	case int16:
		return normalizeGachaTicketSignedInteger(int64(typed))
	case int32:
		return normalizeGachaTicketSignedInteger(int64(typed))
	case int64:
		return normalizeGachaTicketSignedInteger(typed)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint8:
		return strconv.FormatUint(uint64(typed), 10)
	case uint16:
		return strconv.FormatUint(uint64(typed), 10)
	case uint32:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case float32:
		return normalizeGachaTicketFloat(float64(typed), 32)
	case float64:
		return normalizeGachaTicketFloat(typed, 64)
	default:
		return ""
	}
}

func normalizeGachaTicketIntegerText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return ""
		}
	}

	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok || parsed.Sign() < 0 {
		return ""
	}
	return parsed.String()
}

func normalizeGachaTicketSignedInteger(value int64) string {
	if value < 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

func normalizeGachaTicketFloat(value float64, bitSize int) string {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || math.Trunc(value) != value {
		return ""
	}
	return normalizeGachaTicketIntegerText(strconv.FormatFloat(value, 'f', -1, bitSize))
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

func rateChoiceValuesEqual(left any, right any) bool {
	if left == nil || right == nil {
		return false
	}

	leftNumber, leftOK := rateChoiceNumericValue(left)
	rightNumber, rightOK := rateChoiceNumericValue(right)
	if leftOK && rightOK {
		return leftNumber.Cmp(rightNumber) == 0
	}
	if leftOK != rightOK {
		return false
	}

	return shared.NormalizeAnyID(left) == shared.NormalizeAnyID(right)
}

func buildRateChoiceWishResponse(record map[string]any) (shared.GachaRateChoiceWishResponse, bool) {
	lotteryType, ok := rateChoiceStringValue(record["lotteryType"])
	if !ok {
		return shared.GachaRateChoiceWishResponse{}, false
	}
	selectCount, ok := rateChoiceIntegerValue(record["selectCount"])
	if !ok {
		return shared.GachaRateChoiceWishResponse{}, false
	}
	seq, ok := rateChoiceIntegerValue(record["seq"])
	if !ok {
		return shared.GachaRateChoiceWishResponse{}, false
	}

	return shared.GachaRateChoiceWishResponse{
		ID:          record["id"],
		GroupID:     record["groupId"],
		LotteryType: lotteryType,
		SelectCount: selectCount,
		Seq:         seq,
	}, true
}

func rateChoiceStringValue(value any) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}

	text = strings.TrimSpace(text)
	return text, text != ""
}

func compareRateChoiceValues(left any, right any) int {
	if left == nil || right == nil {
		switch {
		case left == nil && right == nil:
			return 0
		case left == nil:
			return 1
		default:
			return -1
		}
	}

	leftNumber, leftOK := rateChoiceNumericValue(left)
	rightNumber, rightOK := rateChoiceNumericValue(right)
	if leftOK && rightOK {
		return leftNumber.Cmp(rightNumber)
	}
	if leftOK != rightOK {
		if leftOK {
			return -1
		}
		return 1
	}

	leftText := shared.NormalizeComparableText(left)
	rightText := shared.NormalizeComparableText(right)
	switch {
	case leftText < rightText:
		return -1
	case leftText > rightText:
		return 1
	default:
		return 0
	}
}

func rateChoiceNumericValue(value any) (*big.Rat, bool) {
	switch typed := value.(type) {
	case json.Number:
		return parseRateChoiceNumericString(typed.String())
	case int:
		return new(big.Rat).SetInt64(int64(typed)), true
	case int8:
		return new(big.Rat).SetInt64(int64(typed)), true
	case int16:
		return new(big.Rat).SetInt64(int64(typed)), true
	case int32:
		return new(big.Rat).SetInt64(int64(typed)), true
	case int64:
		return new(big.Rat).SetInt64(typed), true
	case uint:
		return new(big.Rat).SetUint64(uint64(typed)), true
	case uint8:
		return new(big.Rat).SetUint64(uint64(typed)), true
	case uint16:
		return new(big.Rat).SetUint64(uint64(typed)), true
	case uint32:
		return new(big.Rat).SetUint64(uint64(typed)), true
	case uint64:
		return new(big.Rat).SetUint64(typed), true
	case float32:
		return parseRateChoiceFloat(float64(typed), 32)
	case float64:
		return parseRateChoiceFloat(typed, 64)
	case string:
		return parseRateChoiceNumericString(typed)
	default:
		return nil, false
	}
}

func rateChoiceIntegerValue(value any) (int, bool) {
	numeric, ok := rateChoiceNumericValue(value)
	if !ok || numeric.Denom().Cmp(big.NewInt(1)) != 0 {
		return 0, false
	}

	maxInt := int64(^uint(0) >> 1)
	minInt := -maxInt - 1
	numerator := numeric.Num()
	minValue := big.NewInt(minInt)
	maxValue := big.NewInt(maxInt)
	if numerator.Cmp(minValue) < 0 || numerator.Cmp(maxValue) > 0 {
		return 0, false
	}

	return int(numerator.Int64()), true
}

func parseRateChoiceFloat(value float64, bitSize int) (*big.Rat, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, false
	}

	return parseRateChoiceNumericString(strconv.FormatFloat(value, 'g', -1, bitSize))
}

func parseRateChoiceNumericString(value string) (*big.Rat, bool) {
	rat, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	if !ok {
		return nil, false
	}

	return rat, true
}
