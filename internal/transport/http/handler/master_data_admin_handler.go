package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type MasterDataAdminHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
	timeout        time.Duration
}

type masterDataSyncRequest struct {
	Region string `json:"region"`
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
// @Param payload body masterDataSyncRequest false "Optional region-scoped sync payload"
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

	request := masterDataSyncRequest{}
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&request); err != nil {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid sync request payload")
			return
		}
	}

	region := strings.TrimSpace(request.Region)

	ctx, cancel := context.WithTimeout(c.Request.Context(), handler.timeout)
	defer cancel()

	var err error
	if region == "" {
		err = handler.masterDataSync.SyncAll(ctx)
	} else {
		err = handler.masterDataSync.SyncRegion(ctx, region)
	}
	if err != nil {
		if errors.Is(err, usecase.ErrRegionNotFound) {
			response.Error(c, http.StatusNotFound, "MASTER_DATA_REGION_NOT_FOUND", "target region is not configured")
			return
		}
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
// @Param payload body masterDataSyncRequest false "Optional region-scoped force sync payload"
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

	request := masterDataSyncRequest{}
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&request); err != nil {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid sync request payload")
			return
		}
	}

	region := strings.TrimSpace(request.Region)

	ctx, cancel := context.WithTimeout(c.Request.Context(), handler.timeout)
	defer cancel()

	var err error
	if region == "" {
		err = handler.masterDataSync.SyncAllForce(ctx)
	} else {
		err = handler.masterDataSync.SyncRegionForce(ctx, region)
	}
	if err != nil {
		if errors.Is(err, usecase.ErrRegionNotFound) {
			response.Error(c, http.StatusNotFound, "MASTER_DATA_REGION_NOT_FOUND", "target region is not configured")
			return
		}
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
