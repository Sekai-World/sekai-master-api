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
		respondNotReady(c, "startup")
		return
	}
	if handler.db != nil {
		if err := handler.db.PingContext(c.Request.Context()); err != nil {
			respondNotReady(c, "database")
			return
		}
	}
	if handler.role == config.AppRoleServe && handler.masterDataSync != nil {
		handler.serveReady(c)
		return
	}
	response.JSON(c, http.StatusOK, gin.H{"status": "ok"})
}

// serveReady evaluates per-region readiness for the serve role.  A region is
// considered ready when persisted card records exist regardless of sync status.
// HasSuccessfulSync is queried for diagnostics only and its errors do not gate
// readiness.
func (handler *HealthHandler) serveReady(c *gin.Context) {
	configured := handler.masterDataSync.ConfiguredRegions()
	ready := make([]string, 0, len(configured))
	var regionsPendingSync []string

	for _, region := range configured {
		r, err := handler.evaluateRegionReadiness(c.Request.Context(), region)
		if err != nil {
			respondNotReady(c, "master_data")
			return
		}
		if r.ready {
			ready = append(ready, region)
			if r.pendingSync {
				regionsPendingSync = append(regionsPendingSync, region)
			}
		}
	}

	if len(ready) < len(configured) {
		respondNotReadyWithRegions(c, ready)
		return
	}

	body := gin.H{"status": "ok", "ready_regions": ready}
	if len(regionsPendingSync) > 0 {
		body["regions_pending_sync"] = regionsPendingSync
	}
	response.JSON(c, http.StatusOK, body)
}

// regionReadiness holds the per-region evaluation result.
type regionReadiness struct {
	ready       bool
	pendingSync bool
}

// evaluateRegionReadiness checks whether a single region has persisted card
// records.  If records exist it also probes whether a successful sync has been
// observed; a sync error does not cause a readiness failure.
func (handler *HealthHandler) evaluateRegionReadiness(ctx context.Context, region string) (regionReadiness, error) {
	hasRecords, err := handler.masterDataSync.HasEntityRecords(ctx, region, "cards")
	if err != nil {
		return regionReadiness{}, err
	}
	if !hasRecords {
		return regionReadiness{}, nil
	}
	r := regionReadiness{ready: true}
	hasSync, syncErr := handler.masterDataSync.HasSuccessfulSync(ctx, region)
	if syncErr == nil && !hasSync {
		r.pendingSync = true
	}
	return r, nil
}

func respondNotReady(c *gin.Context, reason string) {
	response.JSON(c, http.StatusServiceUnavailable, gin.H{"status": "not_ready", "reason": reason})
}

func respondNotReadyWithRegions(c *gin.Context, readyRegions []string) {
	response.JSON(c, http.StatusServiceUnavailable, gin.H{
		"status":        "not_ready",
		"reason":        "master_data",
		"ready_regions": readyRegions,
	})
}
