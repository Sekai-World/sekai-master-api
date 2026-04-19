package system

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type VersionsHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
}

func NewVersionsHandler(masterDataSync *usecase.MasterDataSyncUsecase) *VersionsHandler {
	return &VersionsHandler{masterDataSync: masterDataSync}
}

// ByRegion godoc
// @Summary Get cached version.json by region
// @Tags system
// @Produce json
// @Param region path string true "Region"
// @Success 200 {object} map[string]interface{}
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
