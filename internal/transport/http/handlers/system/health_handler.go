package system

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/startup"
	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

// MasterDataReadinessChecker is the subset of MasterDataSyncUsecase used by
// the readiness probe to decide whether persisted master data is available for
// the serve role.
type MasterDataReadinessChecker interface {
	ConfiguredRegions() []string
	HasSuccessfulSync(ctx context.Context, region string) (bool, error)
	HasEntityRecords(ctx context.Context, region string, entity string) (bool, error)
}

type HealthHandler struct {
	db             *sql.DB
	role           config.AppRole
	startupState   *startup.State
	masterDataSync MasterDataReadinessChecker
}

func NewHealthHandler(db *sql.DB, role config.AppRole, startupState *startup.State, masterDataSync *usecase.MasterDataSyncUsecase) *HealthHandler {
	h := &HealthHandler{db: db, role: role, startupState: startupState}
	if masterDataSync != nil {
		h.masterDataSync = masterDataSync
	}
	return h
}

// Check godoc
// @Summary Get health status
// @Tags system
// @Produce json
// @Success 200 {object} shared.HealthResponse
// @Router /health [get]
func (handler *HealthHandler) Check(c *gin.Context) {
	databaseStatus := "up"
	if handler.db != nil {
		if err := handler.db.Ping(); err != nil {
			databaseStatus = "down"
		}
	}

	response.JSON(c, http.StatusOK, gin.H{
		"status":   "ok",
		"database": databaseStatus,
	})
}

func (handler *HealthHandler) Live(c *gin.Context) {
	response.JSON(c, http.StatusOK, gin.H{"status": "ok"})
}

func (handler *HealthHandler) Startup(c *gin.Context) {
	if handler.startupState != nil && !handler.startupState.Ready() {
		response.JSON(c, http.StatusServiceUnavailable, gin.H{"status": "starting"})
		return
	}
	response.JSON(c, http.StatusOK, gin.H{"status": "ok"})
}

func (handler *HealthHandler) Ready(c *gin.Context) {
	if handler.startupState != nil && !handler.startupState.Ready() {
		response.JSON(c, http.StatusServiceUnavailable, gin.H{"status": "not_ready", "reason": "startup"})
		return
	}
	if handler.db != nil {
		if err := handler.db.PingContext(c.Request.Context()); err != nil {
			response.JSON(c, http.StatusServiceUnavailable, gin.H{"status": "not_ready", "reason": "database"})
			return
		}
	}
	if handler.role == config.AppRoleServe && handler.masterDataSync != nil {
		configured := handler.masterDataSync.ConfiguredRegions()
		ready := make([]string, 0, len(configured))
		var regionsPendingSync []string
		for _, region := range configured {
			hasRecords, err := handler.masterDataSync.HasEntityRecords(c.Request.Context(), region, "cards")
			if err != nil {
				response.JSON(c, http.StatusServiceUnavailable, gin.H{"status": "not_ready", "reason": "master_data"})
				return
			}
			if hasRecords {
				ready = append(ready, region)
				hasSync, syncErr := handler.masterDataSync.HasSuccessfulSync(c.Request.Context(), region)
				if syncErr == nil && !hasSync {
					regionsPendingSync = append(regionsPendingSync, region)
				}
			}
		}
		if len(ready) < len(configured) {
			response.JSON(c, http.StatusServiceUnavailable, gin.H{"status": "not_ready", "reason": "master_data", "ready_regions": ready})
			return
		}
		body := gin.H{"status": "ok", "ready_regions": ready}
		if len(regionsPendingSync) > 0 {
			body["regions_pending_sync"] = regionsPendingSync
		}
		response.JSON(c, http.StatusOK, body)
		return
	}
	response.JSON(c, http.StatusOK, gin.H{"status": "ok"})
}
