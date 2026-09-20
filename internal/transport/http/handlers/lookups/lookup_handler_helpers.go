package lookups

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

const maxLookupPageSize = 100

func parseLookupPagination(c *gin.Context) (int, int, bool) {
	page := 1
	if rawPage := strings.TrimSpace(c.Query("page")); rawPage != "" {
		parsedPage, err := strconv.Atoi(rawPage)
		if err != nil || parsedPage <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page must be a positive integer")
			return 0, 0, false
		}
		page = parsedPage
	}

	pageSize := 20
	if rawPageSize := strings.TrimSpace(c.Query("page_size")); rawPageSize != "" {
		parsedPageSize, err := strconv.Atoi(rawPageSize)
		if err != nil || parsedPageSize <= 0 || parsedPageSize > maxLookupPageSize {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page_size must be a positive integer no greater than 100")
			return 0, 0, false
		}
		pageSize = parsedPageSize
	}

	return page, pageSize, true
}

func lookupPaginationResponse(page, pageSize, total int) shared.PaginationResponse {
	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	return shared.PaginationResponse{
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		HasNext:    page < totalPages,
	}
}

func parseTypedLookupRegion(c *gin.Context, masterDataSync *usecase.MasterDataSyncUsecase) (string, bool) {
	region := strings.ToLower(strings.TrimSpace(c.Param("region")))
	if region == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region is invalid")
		return "", false
	}

	configuredRegions := masterDataSync.ConfiguredRegions()
	if len(configuredRegions) > 0 {
		for _, configuredRegion := range configuredRegions {
			if region == configuredRegion {
				return region, true
			}
		}

		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region is not configured")
		return "", false
	}

	if !isValidLookupRegionCode(region) {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region is invalid")
		return "", false
	}

	return region, true
}

func isValidLookupRegionCode(region string) bool {
	if region == "" {
		return false
	}

	for _, character := range region {
		isLowercaseLetter := character >= 'a' && character <= 'z'
		isDigit := character >= '0' && character <= '9'
		if isLowercaseLetter || isDigit || character == '-' || character == '_' {
			continue
		}
		return false
	}

	return true
}

func parseTypedLookupID(c *gin.Context) (string, bool) {
	rawID := strings.TrimSpace(c.Param("id"))
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "id must be a positive integer")
		return "", false
	}

	return strconv.FormatInt(id, 10), true
}

func lookupInt64(value any) (int64, bool) {
	switch number := value.(type) {
	case int:
		return int64(number), true
	case int32:
		return int64(number), true
	case int64:
		return number, true
	case float32:
		return lookupFloat64Int64(float64(number))
	case float64:
		return lookupFloat64Int64(number)
	case json.Number:
		parsed, err := strconv.ParseInt(number.String(), 10, 64)
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(number), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func lookupFloat64Int64(number float64) (int64, bool) {
	if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number {
		return 0, false
	}
	if number < float64(math.MinInt64) || number >= float64(math.MaxInt64) {
		return 0, false
	}

	return int64(number), true
}

func lookupRequiredInt64(value any) int64 {
	parsed, _ := lookupInt64(value)
	return parsed
}

func lookupOptionalInt64(value any) *int64 {
	parsed, ok := lookupInt64(value)
	if !ok {
		return nil
	}

	return &parsed
}

func lookupOptionalFloat64(value any) *float64 {
	var parsed float64
	switch number := value.(type) {
	case int:
		parsed = float64(number)
	case int32:
		parsed = float64(number)
	case int64:
		parsed = float64(number)
	case float32:
		parsed = float64(number)
	case float64:
		parsed = number
	case json.Number:
		value, err := strconv.ParseFloat(number.String(), 64)
		if err != nil {
			return nil
		}
		parsed = value
	case string:
		value, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
		if err != nil {
			return nil
		}
		parsed = value
	default:
		return nil
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return nil
	}

	return &parsed
}

func lookupString(value any) string {
	text, _ := value.(string)
	return text
}

func lookupOptionalString(value any) *string {
	text, ok := value.(string)
	if !ok {
		return nil
	}

	return &text
}

func lookupRecord(value any) (map[string]any, bool) {
	record, ok := value.(map[string]any)
	return record, ok
}
