package gamenews

import (
	"encoding/json"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

const gameNewsEntity = "userInformations"

type GameNewsHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
}

func NewGameNewsHandler(masterDataSync *usecase.MasterDataSyncUsecase) *GameNewsHandler {
	return &GameNewsHandler{masterDataSync: masterDataSync}
}

// List godoc
// @Summary List current game news
// @Tags gameNews
// @Produce json
// @Param region path string true "Region"
// @Param includeAll query bool false "Include all game news records"
// @Success 200 {object} shared.GameNewsListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /game-news/{region}/list [get]
func (handler *GameNewsHandler) List(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	if region == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region is required")
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, gameNewsEntity) {
		return
	}

	includeAll, ok := parseIncludeAll(c)
	if !ok {
		return
	}

	records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, gameNewsEntity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "GAME_NEWS_QUERY_ERROR", "failed to list game news")
		return
	}
	if records == nil {
		records = []map[string]any{}
	}
	if !includeAll {
		records = filterCurrentGameNews(records, time.Now().UTC())
	}
	records = normalizeGameNewsRecords(records)

	response.JSON(c, http.StatusOK, gin.H{"items": records})
}

func parseIncludeAll(c *gin.Context) (bool, bool) {
	rawValue := strings.TrimSpace(c.Query("includeAll"))
	if rawValue == "" {
		return false, true
	}

	parsed, err := strconv.ParseBool(rawValue)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "includeAll must be a boolean")
		return false, false
	}

	return parsed, true
}

func filterCurrentGameNews(records []map[string]any, now time.Time) []map[string]any {
	nowMillis := now.UTC().UnixMilli()
	filtered := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if !isCurrentGameNewsRecord(record, nowMillis) {
			continue
		}
		filtered = append(filtered, record)
	}

	return filtered
}

func isCurrentGameNewsRecord(record map[string]any, nowMillis int64) bool {
	startAt, exists := record["startAt"]
	if !exists {
		return false
	}
	startMillis, ok := parseGameNewsTimestamp(startAt)
	if !ok || startMillis > nowMillis {
		return false
	}

	endAt, exists := record["endAt"]
	if !exists || endAt == nil {
		return true
	}
	endMillis, ok := parseGameNewsTimestamp(endAt)
	if !ok {
		return false
	}

	return endMillis > nowMillis
}

func normalizeGameNewsRecords(records []map[string]any) []map[string]any {
	normalized := make([]map[string]any, 0, len(records))
	for _, record := range records {
		normalizedRecord, ok := normalizeGameNewsRecord(record)
		if !ok {
			continue
		}
		normalized = append(normalized, normalizedRecord)
	}

	return normalized
}

func normalizeGameNewsRecord(record map[string]any) (map[string]any, bool) {
	if record == nil {
		return nil, false
	}

	startAt, exists := record["startAt"]
	if !exists {
		return nil, false
	}
	startMillis, ok := parseGameNewsTimestamp(startAt)
	if !ok {
		return nil, false
	}

	normalized := make(map[string]any, len(record))
	for key, value := range record {
		normalized[key] = value
	}
	normalized["startAt"] = startMillis

	if endAt, exists := record["endAt"]; exists {
		if endAt == nil {
			normalized["endAt"] = nil
		} else if endMillis, ok := parseGameNewsTimestamp(endAt); ok {
			normalized["endAt"] = endMillis
		} else {
			delete(normalized, "endAt")
		}
	}

	return normalized, true
}

func parseGameNewsTimestamp(value any) (int64, bool) {
	var numeric string
	switch typed := value.(type) {
	case int:
		numeric = strconv.FormatInt(int64(typed), 10)
	case int8:
		numeric = strconv.FormatInt(int64(typed), 10)
	case int16:
		numeric = strconv.FormatInt(int64(typed), 10)
	case int32:
		numeric = strconv.FormatInt(int64(typed), 10)
	case int64:
		numeric = strconv.FormatInt(typed, 10)
	case uint:
		numeric = strconv.FormatUint(uint64(typed), 10)
	case uint8:
		numeric = strconv.FormatUint(uint64(typed), 10)
	case uint16:
		numeric = strconv.FormatUint(uint64(typed), 10)
	case uint32:
		numeric = strconv.FormatUint(uint64(typed), 10)
	case uint64:
		numeric = strconv.FormatUint(typed, 10)
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return 0, false
		}
		numeric = strconv.FormatFloat(float64(typed), 'g', -1, 32)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return 0, false
		}
		numeric = strconv.FormatFloat(typed, 'g', -1, 64)
	case json.Number:
		numeric = typed.String()
	case string:
		numeric = strings.TrimSpace(typed)
		if numeric == "" {
			return 0, false
		}
		if parsed, ok := parseGameNewsNumericTimestamp(numeric); ok {
			return parsed, true
		}
		return parseGameNewsDateTimestamp(numeric)
	default:
		return 0, false
	}

	return parseGameNewsNumericTimestamp(numeric)
}

func parseGameNewsNumericTimestamp(value string) (int64, bool) {
	parsedFloat, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsedFloat) || math.IsInf(parsedFloat, 0) {
		return 0, false
	}

	timestamp, ok := new(big.Rat).SetString(value)
	if !ok {
		return 0, false
	}

	absTimestamp := new(big.Rat).Abs(timestamp)
	switch {
	case absTimestamp.Cmp(big.NewRat(100_000_000_000, 1)) < 0:
		timestamp.Mul(timestamp, big.NewRat(1_000, 1))
	case absTimestamp.Cmp(big.NewRat(100_000_000_000_000_000, 1)) >= 0:
		timestamp.Quo(timestamp, big.NewRat(1_000_000, 1))
	case absTimestamp.Cmp(big.NewRat(100_000_000_000_000, 1)) >= 0:
		timestamp.Quo(timestamp, big.NewRat(1_000, 1))
	}

	wholeMillis := new(big.Int).Quo(timestamp.Num(), timestamp.Denom())
	if !wholeMillis.IsInt64() {
		return 0, false
	}

	return wholeMillis.Int64(), true
}

func parseGameNewsDateTimestamp(value string) (int64, bool) {
	parsedTime, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		parsedTime, err = time.Parse(time.RFC3339, value)
	}
	if err != nil {
		for _, layout := range []string{
			"2006-01-02 15:04:05",
			"2006-01-02 15:04:05Z07:00",
			"2006-01-02 15:04:05 -0700",
		} {
			parsedTime, err = time.Parse(layout, value)
			if err == nil {
				return parsedTime.UnixMilli(), true
			}
		}

		return 0, false
	}

	return parsedTime.UnixMilli(), true
}
