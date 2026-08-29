package http

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	_ "sekai-master-api/internal/transport/http/swaggerdocs"

	"sekai-master-api/internal/auth"
	"sekai-master-api/internal/config"
	"sekai-master-api/internal/startup"
	adminhandlers "sekai-master-api/internal/transport/http/handlers/admin"
	cardhandlers "sekai-master-api/internal/transport/http/handlers/cards"
	eventhandlers "sekai-master-api/internal/transport/http/handlers/events"
	gachahandlers "sekai-master-api/internal/transport/http/handlers/gachas"
	lookuphandlers "sekai-master-api/internal/transport/http/handlers/lookups"
	musichandlers "sekai-master-api/internal/transport/http/handlers/musics"
	systemhandlers "sekai-master-api/internal/transport/http/handlers/system"
	virtuallivehandlers "sekai-master-api/internal/transport/http/handlers/virtuallives"
	"sekai-master-api/internal/transport/http/middleware"
	"sekai-master-api/internal/usecase"
)

func NewRouter(cfg config.Config, db *sql.DB, tokenVerifier auth.TokenVerifier, masterDataSync *usecase.MasterDataSyncUsecase, masterDataEvents *usecase.MasterDataEventHub, startupState *startup.State, lifecycleCtx context.Context) (*gin.Engine, *systemhandlers.GitHubWebhookHandler, error) {
	router := gin.New()

	httpMetrics, err := middleware.HTTPMetrics()
	if err != nil {
		return nil, nil, err
	}

	router.Use(middleware.RequestID())
	if cfg.OTELEnabled && cfg.OTELTracingEnabled {
		router.Use(otelgin.Middleware(cfg.OTELServiceName, otelgin.WithFilter(func(request *http.Request) bool {
			if request == nil || request.URL == nil {
				return false
			}
			return !strings.HasPrefix(request.URL.Path, "/docs")
		})))
	}
	router.Use(httpMetrics)
	router.Use(middleware.AccessLog())
	router.Use(middleware.StartupGate(startupState))
	router.Use(middleware.RecoveryLog())

	healthHandler := systemhandlers.NewHealthHandler(db, cfg.Role, startupState, masterDataSync)
	versionsHandler := systemhandlers.NewVersionsHandler(masterDataSync)
	buildInfoHandler := systemhandlers.NewBuildInfoHandler()
	gitHubWebhookHandler := systemhandlers.NewGitHubWebhookHandler(
		cfg.MasterDataSources,
		masterDataSync,
		time.Duration(cfg.MasterDataSyncTimeout)*time.Second,
		cfg.MasterDataGitHubWebhookSecret,
	)
	cardHandler := cardhandlers.NewCardHandler(masterDataSync)
	musicHandler := musichandlers.NewMusicHandler(masterDataSync)
	eventHandler := eventhandlers.NewEventHandler(masterDataSync)
	gachaHandler := gachahandlers.NewGachaHandler(masterDataSync)
	lookupHandler := lookuphandlers.NewLookupHandler(masterDataSync)
	virtualLiveHandler := virtuallivehandlers.NewVirtualLiveHandler(masterDataSync)
	masterDataAdminHandler := adminhandlers.NewMasterDataAdminHandler(masterDataSync, startupState)

	role := cfg.Role
	admin, err := setupAdminHandlers(cfg, masterDataEvents)
	if err != nil {
		return nil, nil, err
	}

	if isSwaggerEnabledEnv(cfg.AppEnv) {
		router.GET("/docs/*any", swaggerHandler())
	}

	router.GET("/livez", healthHandler.Live)
	router.GET("/startupz", healthHandler.Startup)
	router.GET("/readyz", healthHandler.Ready)

	v1 := router.Group("/api/v1")

	registerRoleRoutes(&routeDeps{
		router:                 router,
		v1:                     v1,
		role:                   role,
		healthHandler:          healthHandler,
		versionsHandler:        versionsHandler,
		buildInfoHandler:       buildInfoHandler,
		cardHandler:            cardHandler,
		musicHandler:           musicHandler,
		eventHandler:           eventHandler,
		gachaHandler:           gachaHandler,
		lookupHandler:          lookupHandler,
		virtualLiveHandler:     virtualLiveHandler,
		gitHubWebhookHandler:   gitHubWebhookHandler,
		tokenVerifier:          tokenVerifier,
		admin:                  admin,
		masterDataAdminHandler: masterDataAdminHandler,
	}, lifecycleCtx)

	return router, gitHubWebhookHandler, nil
}

func isSwaggerEnabledEnv(appEnv string) bool {
	normalized := strings.ToLower(strings.TrimSpace(appEnv))
	return normalized == "development" || normalized == "dev" || normalized == "test"
}

func swaggerHandler() gin.HandlerFunc {
	handler := ginSwagger.WrapHandler(swaggerFiles.Handler, ginSwagger.URL("/docs/openapi.json"))

	return func(ctx *gin.Context) {
		if ctx.Request.URL.Path == "/docs/doc.json" {
			ctx.String(http.StatusNotFound, http.StatusText(http.StatusNotFound))
			return
		}

		if ctx.Request.URL.Path == "/docs/openapi.json" {
			originalPath := ctx.Request.URL.Path
			originalRequestURI := ctx.Request.RequestURI
			ctx.Request.URL.Path = "/docs/doc.json"
			ctx.Request.RequestURI = strings.Replace(originalRequestURI, "/docs/openapi.json", "/docs/doc.json", 1)
			defer func() {
				ctx.Request.URL.Path = originalPath
				ctx.Request.RequestURI = originalRequestURI
			}()
		}

		handler(ctx)
	}
}

type adminHandlerBundle struct {
	claimAuthorizer  *auth.AdminClaimAuthorizer
	profile          *adminhandlers.ProfileHandler
	adminUI          *adminhandlers.AdminUIHandler
	adminLogin       *adminhandlers.AdminLoginHandler
	masterDataEvents *adminhandlers.MasterDataEventHandler
}

// setupAdminHandlers builds the operational/admin handler set, which is only
// needed by the standalone and control roles (the control role owns sync and
// serves the admin SSE dashboard). Extracted from NewRouter to keep its
// cognitive complexity within budget.
func setupAdminHandlers(cfg config.Config, masterDataEvents *usecase.MasterDataEventHub) (*adminHandlerBundle, error) {
	if cfg.Role != config.AppRoleStandalone && cfg.Role != config.AppRoleControl {
		return nil, nil
	}

	claimAuthorizer := auth.NewAdminClaimAuthorizer(cfg.OIDCAdminClaim, cfg.OIDCAdminClaimValues)
	profile := adminhandlers.NewProfileHandler(cfg.AppEnv, claimAuthorizer)
	adminUI := adminhandlers.NewAdminUIHandler(cfg)
	adminLogin, err := adminhandlers.NewAdminLoginHandler(cfg)
	if err != nil {
		return nil, err
	}
	masterDataEventsHandler := adminhandlers.NewMasterDataEventHandler(masterDataEvents)

	return &adminHandlerBundle{
		claimAuthorizer:  claimAuthorizer,
		profile:          profile,
		adminUI:          adminUI,
		adminLogin:       adminLogin,
		masterDataEvents: masterDataEventsHandler,
	}, nil
}

// routeDeps bundles the handlers and runtime context needed to wire role-specific
// routes. Bundled into a single struct to keep registerRoleRoutes's parameter
// list within budget (go:S107) while leaving route contracts unchanged.
type routeDeps struct {
	router                 *gin.Engine
	v1                     *gin.RouterGroup
	role                   config.AppRole
	healthHandler          *systemhandlers.HealthHandler
	versionsHandler        *systemhandlers.VersionsHandler
	buildInfoHandler       *systemhandlers.BuildInfoHandler
	cardHandler            *cardhandlers.CardHandler
	musicHandler           *musichandlers.MusicHandler
	eventHandler           *eventhandlers.EventHandler
	gachaHandler           *gachahandlers.GachaHandler
	lookupHandler          *lookuphandlers.LookupHandler
	virtualLiveHandler     *virtuallivehandlers.VirtualLiveHandler
	gitHubWebhookHandler   *systemhandlers.GitHubWebhookHandler
	tokenVerifier          auth.TokenVerifier
	admin                  *adminHandlerBundle
	masterDataAdminHandler *adminhandlers.MasterDataAdminHandler
}

// registerRoleRoutes wires role-specific routes. Extracted from NewRouter to keep
// its cognitive complexity within budget; route contracts are unchanged. The
// lifecycle context is passed explicitly (not stored in the struct, godre/S8242)
// because a context.Context must not live in a long-lived struct.
func registerRoleRoutes(deps *routeDeps, lifecycleCtx context.Context) {
	// Public read/query workload.
	if deps.role == config.AppRoleStandalone || deps.role == config.AppRoleServe {
		registerPublicRoutes(deps.v1, deps.healthHandler, deps.versionsHandler, deps.cardHandler, deps.musicHandler, deps.eventHandler, deps.gachaHandler, deps.lookupHandler, deps.virtualLiveHandler)
		deps.v1.GET("/build-info", deps.buildInfoHandler.BuildInfo)
	}

	// The control (operational) role must not expose general public data/query
	// endpoints, but it still exposes /api/v1/health for orchestration health
	// checks.
	if deps.role == config.AppRoleControl {
		deps.v1.GET("/health", deps.healthHandler.Check)
	}

	// Internal write-triggering surface (GitHub webhook sync). Exposed only by
	// standalone and the control (operational) role that owns sync.
	if deps.role == config.AppRoleStandalone || deps.role == config.AppRoleControl {
		registerInternalRoutes(deps.v1, deps.gitHubWebhookHandler, lifecycleCtx)
	}

	// Operational/admin workload.
	if deps.role == config.AppRoleStandalone || deps.role == config.AppRoleControl {
		registerAdminRoutes(
			deps.router,
			deps.v1,
			deps.tokenVerifier,
			deps.admin.claimAuthorizer,
			deps.admin.adminUI,
			deps.admin.adminLogin,
			deps.admin.profile,
			deps.admin.masterDataEvents,
			deps.masterDataAdminHandler,
		)
	}
}
