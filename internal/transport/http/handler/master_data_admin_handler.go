package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type MasterDataAdminHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
	timeout        time.Duration
}

func NewMasterDataAdminHandler(masterDataSync *usecase.MasterDataSyncUsecase, timeout time.Duration) *MasterDataAdminHandler {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	return &MasterDataAdminHandler{
		masterDataSync: masterDataSync,
		timeout:        timeout,
	}
}

func (handler *MasterDataAdminHandler) Sync(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_SYNC_DISABLED", "master data sync is not configured")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), handler.timeout)
	defer cancel()

	if err := handler.masterDataSync.SyncAll(ctx); err != nil {
		response.Error(c, http.StatusInternalServerError, "MASTER_DATA_SYNC_FAILED", "master data sync completed with errors: "+err.Error())
		return
	}

	statuses, err := handler.masterDataSync.Status(ctx)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MASTER_DATA_STATUS_ERROR", "master data sync succeeded but failed to load status")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{
		"status": "ok",
		"items":  statuses,
	})
}
