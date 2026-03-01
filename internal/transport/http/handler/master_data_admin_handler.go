package handler

import (
	"context"
	"errors"
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

// Sync godoc
// @Summary Trigger master-data sync
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} MasterDataSyncResponse
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Router /admin/master-data/sync [post]
func (handler *MasterDataAdminHandler) Sync(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_SYNC_DISABLED", "master data sync is not configured")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), handler.timeout)
	defer cancel()

	if err := handler.masterDataSync.SyncAll(ctx); err != nil {
		if errors.Is(err, usecase.ErrSyncInProgress) {
			response.Error(c, http.StatusConflict, "MASTER_DATA_SYNC_RUNNING", "master data sync is already running")
			return
		}
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

// ForceSync godoc
// @Summary Trigger force master-data sync
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} MasterDataSyncResponse
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Router /admin/master-data/sync/force [post]
func (handler *MasterDataAdminHandler) ForceSync(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_SYNC_DISABLED", "master data sync is not configured")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), handler.timeout)
	defer cancel()

	if err := handler.masterDataSync.SyncAllForce(ctx); err != nil {
		if errors.Is(err, usecase.ErrSyncInProgress) {
			response.Error(c, http.StatusConflict, "MASTER_DATA_SYNC_RUNNING", "master data sync is already running")
			return
		}
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
