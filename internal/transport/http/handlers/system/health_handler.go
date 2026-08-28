package system

import (
	"context"
	"database/sql"
	"net/http"
	"time"

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
	RedisReady(ctx context.Context) (bool, error)
	RegionVersionReady(ctx context.Context, region string) (bool, error)
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
		dbCtx, dbCancel := context.WithTimeout(c.Request.Context(), readinessProbeTimeout)
		defer dbCancel()
		if err := handler.db.PingContext(dbCtx); err != nil {
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

// readinessProbeTimeout bounds the readiness scan so a slow PostgreSQL or Redis
// cannot hang the Kubernetes readiness probe indefinitely. The check is
// read-only: it never writes to PostgreSQL or Redis.
const readinessProbeTimeout = 5 * time.Second

// serveReady evaluates a bounded, read-only readiness check for the serve role.
// It verifies Redis connectivity once, then for each configured region checks
// that persisted card records AND version metadata are available. The response
// enumerates affected (unready) regions but never includes secrets such as
// database URLs, Redis credentials, or source repository references.
func (handler *HealthHandler) serveReady(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), readinessProbeTimeout)
	defer cancel()

	configured := handler.masterDataSync.ConfiguredRegions()

	if redisReady, _ := handler.masterDataSync.RedisReady(ctx); !redisReady {
		// Without Redis the serve pod cannot serve any configured region. The
		// response reason "redis" already signals the failing dependency.
		respondNotReadyWithRegions(c, "redis", configured, []string{})
		return
	}

	ready := make([]string, 0, len(configured))
	unready := make([]string, 0, len(configured))
	var regionsPendingSync []string

	for _, region := range configured {
		r, err := handler.evaluateRegionReadiness(ctx, region)
		if err != nil {
			// HasEntityRecords and RegionVersionReady both read from Redis, so a
			// transport/storage error here means Redis is not reachable. Report
			// the redis dependency rather than master_data.
			respondNotReadyWithRegions(c, "redis", configured, []string{})
			return
		}
		if r.ready {
			ready = append(ready, region)
		} else {
			unready = append(unready, region)
		}
		if r.ready && r.pendingSync {
			regionsPendingSync = append(regionsPendingSync, region)
		}
	}

	if len(ready) < len(configured) {
		respondNotReadyWithRegions(c, "master_data", unready, ready)
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

// evaluateRegionReadiness checks whether a single region is ready. A region is
// ready when it has persisted card records AND version metadata available. If
// records exist it also probes whether a successful sync has been observed; a
// sync error does not cause a readiness failure (it only contributes to the
// diagnostic regions_pending_sync list).
func (handler *HealthHandler) evaluateRegionReadiness(ctx context.Context, region string) (regionReadiness, error) {
	hasRecords, err := handler.masterDataSync.HasEntityRecords(ctx, region, "cards")
	if err != nil {
		return regionReadiness{}, err
	}
	if !hasRecords {
		return regionReadiness{}, nil
	}

	versionReady, versionErr := handler.masterDataSync.RegionVersionReady(ctx, region)
	if versionErr != nil {
		return regionReadiness{}, versionErr
	}
	if !versionReady {
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

// respondNotReadyWithRegions reports the pod not ready and enumerates the regions
// that are (and are not) ready. Both keys are always present for response
// compatibility: ready_regions is emitted as [] when no region is ready. Secrets
// such as database connection strings, Redis credentials, and source references are
// never included.
func respondNotReadyWithRegions(c *gin.Context, reason string, unreadyRegions, readyRegions []string) {
	body := gin.H{
		"status":          "not_ready",
		"reason":          reason,
		"unready_regions": unreadyRegions,
		"ready_regions":   readyRegions,
	}
	response.JSON(c, http.StatusServiceUnavailable, body)
}
