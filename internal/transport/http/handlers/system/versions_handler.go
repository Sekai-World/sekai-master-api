package system

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
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

	if value, found := masterdata.VersionStringValue(versionMap, "appVersion"); found {
		normalized.AppVersion = value
	}
	if value, found := masterdata.VersionStringValue(versionMap, "assetVersion"); found {
		normalized.AssetVersion = value
	}
	if value, found := masterdata.VersionStringValue(versionMap, "dataVersion"); found {
		normalized.DataVersion = value
	}
	if value, found := masterdata.VersionSmallIntValue(versionMap, "cdnVersion"); found {
		normalized.CdnVersion = &value
	}

	// Reuse the shared contract check so the public /versions response and the
	// serve readiness probe agree on what counts as usable version metadata.
	if !masterdata.IsCompleteVersionPayload(versionMap) {
		return shared.MasterDataVersionsResponse{}, false
	}

	return normalized, true
}
