package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type MasterDataStatusHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
}

func NewMasterDataStatusHandler(masterDataSync *usecase.MasterDataSyncUsecase) *MasterDataStatusHandler {
	return &MasterDataStatusHandler{masterDataSync: masterDataSync}
}

// List godoc
// @Summary Get master-data sync status
// @Tags master-data
// @Produce json
// @Success 200 {object} MasterDataStatusListResponse
// @Failure 500 {object} ErrorResponse
// @Router /master-data/status [get]
func (handler *MasterDataStatusHandler) List(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.JSON(c, http.StatusOK, gin.H{"items": []any{}})
		return
	}

	statuses, err := handler.masterDataSync.Status(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MASTER_DATA_STATUS_ERROR", "failed to load master data sync status")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{"items": statuses})
}
