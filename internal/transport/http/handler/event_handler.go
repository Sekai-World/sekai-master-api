package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type EventHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
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

	response.JSON(c, http.StatusOK, buildEventBase(record))
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

	statuses, err := handler.masterDataSync.Status(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MASTER_DATA_STATUS_ERROR", "failed to check master data sync status")
		return false
	}

	normalizedRegion := strings.TrimSpace(region)
	regionReady := false
	regionFound := false
	for _, status := range statuses {
		if !strings.EqualFold(strings.TrimSpace(status.Region), normalizedRegion) {
			continue
		}
		regionFound = true
		if strings.EqualFold(strings.TrimSpace(status.Status), "success") {
			regionReady = true
			break
		}
	}

	if regionReady {
		return true
	}

	if !regionFound || !regionReady {
		response.Error(c, http.StatusServiceUnavailable, "REGION_DATA_NOT_READY", "region data is updating or unavailable, please try again later")
		return false
	}

	return false
}

func buildEventBase(record map[string]any) map[string]any {
	if record == nil {
		return map[string]any{}
	}

	result := make(map[string]any, len(record))
	for key, value := range record {
		if key == "eventRankingRewardRanges" {
			continue
		}
		result[key] = value
	}

	return result
}
