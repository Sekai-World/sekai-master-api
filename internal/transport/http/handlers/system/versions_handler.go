package system

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type VersionsHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
}

func NewVersionsHandler(masterDataSync *usecase.MasterDataSyncUsecase) *VersionsHandler {
	return &VersionsHandler{masterDataSync: masterDataSync}
}

// AllRegions godoc
// @Summary Get cached versions.json for all configured regions
// @Tags system
// @Produce json
// @Success 200 {object} shared.MasterDataVersionsByRegionResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /versions [get]
func (handler *VersionsHandler) AllRegions(c *gin.Context) {
	if handler == nil || handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	versions := make(shared.MasterDataVersionsByRegionResponse)
	for _, region := range handler.masterDataSync.ConfiguredRegions() {
		version, found, err := handler.masterDataSync.VersionByRegion(c.Request.Context(), region)
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "VERSION_QUERY_ERROR", "failed to load region version")
			return
		}
		if !found {
			continue
		}

		normalized, ok := normalizeMasterDataVersionPayload(version)
		if !ok {
			continue
		}
		versions[region] = normalized
	}

	response.JSON(c, http.StatusOK, versions)
}

// ByRegion godoc
// @Summary Get cached versions.json by region
// @Tags system
// @Produce json
// @Param region path string true "Region"
// @Success 200 {object} shared.MasterDataVersionsResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /versions/{region} [get]
func (handler *VersionsHandler) ByRegion(c *gin.Context) {
	if handler == nil || handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	if region == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region is required")
		return
	}

	version, found, err := handler.masterDataSync.VersionByRegion(c.Request.Context(), region)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "VERSION_QUERY_ERROR", "failed to load region version")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "VERSION_NOT_FOUND", "version not found")
		return
	}

	response.JSON(c, http.StatusOK, version)
}

func normalizeMasterDataVersionPayload(payload any) (shared.MasterDataVersionsResponse, bool) {
	versionMap, ok := payload.(map[string]any)
	if !ok {
		return shared.MasterDataVersionsResponse{}, false
	}

	var normalized shared.MasterDataVersionsResponse

	if value, found := versionValue(versionMap, "appVersion"); found {
		normalized.AppVersion = value
	}
	if value, found := versionValue(versionMap, "assetVersion"); found {
		normalized.AssetVersion = value
	}
	if value, found := versionValue(versionMap, "dataVersion"); found {
		normalized.DataVersion = value
	}
	if value, found := versionSmallIntValue(versionMap, "cdnVersion"); found {
		normalized.CdnVersion = &value
	}

	if normalized.AppVersion == "" && normalized.AssetVersion == "" && normalized.DataVersion == "" && normalized.CdnVersion == nil {
		return shared.MasterDataVersionsResponse{}, false
	}

	return normalized, true
}

func versionValue(payload map[string]any, key string) (string, bool) {
	value, ok := payload[key]
	if !ok {
		return "", false
	}

	switch typedValue := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typedValue)
		if trimmed == "" {
			return "", false
		}
		return trimmed, true
	case fmt.Stringer:
		trimmed := strings.TrimSpace(typedValue.String())
		if trimmed == "" {
			return "", false
		}
		return trimmed, true
	default:
		return "", false
	}
}

func versionSmallIntValue(payload map[string]any, key string) (int, bool) {
	value, ok := payload[key]
	if !ok {
		return 0, false
	}

	parsed, ok := parseSmallInt(value)
	if !ok {
		return 0, false
	}

	if parsed < 0 || parsed > 999 {
		return 0, false
	}

	return parsed, true
}

func parseSmallInt(value any) (int, bool) {
	switch typedValue := value.(type) {
	case int:
		return typedValue, true
	case int8:
		return int(typedValue), true
	case int16:
		return int(typedValue), true
	case int32:
		return int(typedValue), true
	case int64:
		converted := int(typedValue)
		if int64(converted) != typedValue {
			return 0, false
		}
		return converted, true
	case uint:
		converted := int(typedValue)
		if uint(converted) != typedValue {
			return 0, false
		}
		return converted, true
	case uint8:
		return int(typedValue), true
	case uint16:
		return int(typedValue), true
	case uint32:
		converted := int(typedValue)
		if uint32(converted) != typedValue {
			return 0, false
		}
		return converted, true
	case uint64:
		converted := int(typedValue)
		if uint64(converted) != typedValue {
			return 0, false
		}
		return converted, true
	case float32:
		asFloat := float64(typedValue)
		if asFloat != math.Trunc(asFloat) {
			return 0, false
		}
		return int(asFloat), true
	case float64:
		if typedValue != math.Trunc(typedValue) {
			return 0, false
		}
		return int(typedValue), true
	case string:
		trimmed := strings.TrimSpace(typedValue)
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.Atoi(trimmed)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}
