package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/startup"
	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type MasterDataAdminHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
	startupState   *startup.State
}

type masterDataSyncRequest struct {
	Region string `json:"region"`
}

func NewMasterDataAdminHandler(masterDataSync *usecase.MasterDataSyncUsecase, startupState *startup.State) *MasterDataAdminHandler {
	return &MasterDataAdminHandler{
		masterDataSync: masterDataSync,
		startupState:   startupState,
	}
}

// Status godoc
// @Summary Get admin master-data sync status
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Success 200 {object} shared.MasterDataAdminStatusResponse
// @Failure 401 {object} shared.ErrorResponse
// @Failure 403 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /admin/master-data/status [get]
func (handler *MasterDataAdminHandler) Status(c *gin.Context) {
	if handler != nil && handler.startupState != nil && !handler.startupState.Ready() {
		regions := []string{}
		if handler.masterDataSync != nil {
			regions = handler.masterDataSync.ConfiguredRegions()
		}

		response.JSON(c, http.StatusOK, gin.H{
			"status":        "ok",
			"items":         []any{},
			"regions":       regions,
			"sync_running":  false,
			"startup_ready": false,
		})
		return
	}

	if handler.masterDataSync == nil {
		response.JSON(c, http.StatusOK, gin.H{
			"status":        "ok",
			"items":         []any{},
			"regions":       []string{},
			"sync_running":  false,
			"startup_ready": true,
		})
		return
	}

	handler.writeStatusResponse(c, c.Request.Context())
}

// Sync godoc
// @Summary Trigger master-data sync
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Param payload body masterDataSyncRequest false "Optional region-scoped sync payload"
// @Success 200 {object} shared.MasterDataSyncResponse
// @Failure 401 {object} shared.ErrorResponse
// @Failure 403 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Router /admin/master-data/sync [post]
func (handler *MasterDataAdminHandler) Sync(c *gin.Context) {
	if handler != nil && handler.startupState != nil && !handler.startupState.Ready() {
		response.Error(c, http.StatusServiceUnavailable, "STARTUP_IN_PROGRESS", "service startup is still in progress")
		return
	}

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
	ctx := c.Request.Context()

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

	handler.writeStatusResponse(c, ctx)
}

// ForceSync godoc
// @Summary Trigger force master-data sync
// @Tags admin
// @Produce json
// @Security BearerAuth
// @Param payload body masterDataSyncRequest false "Optional region-scoped force sync payload"
// @Success 200 {object} shared.MasterDataSyncResponse
// @Failure 401 {object} shared.ErrorResponse
// @Failure 403 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Router /admin/master-data/sync/force [post]
func (handler *MasterDataAdminHandler) ForceSync(c *gin.Context) {
	if handler != nil && handler.startupState != nil && !handler.startupState.Ready() {
		response.Error(c, http.StatusServiceUnavailable, "STARTUP_IN_PROGRESS", "service startup is still in progress")
		return
	}

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
	ctx := c.Request.Context()

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

	handler.writeStatusResponse(c, ctx)
}

func (handler *MasterDataAdminHandler) writeStatusResponse(c *gin.Context, ctx context.Context) {
	statuses, err := handler.masterDataSync.DashboardStatus(ctx)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MASTER_DATA_STATUS_ERROR", "failed to load master data sync status")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{
		"status":        "ok",
		"items":         statuses,
		"regions":       handler.masterDataSync.ConfiguredRegions(),
		"sync_running":  handler.masterDataSync.IsSyncRunning(),
		"startup_ready": true,
	})
}
