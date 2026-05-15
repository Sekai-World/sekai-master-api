package http

import (
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
	lookuphandlers "sekai-master-api/internal/transport/http/handlers/lookups"
	musichandlers "sekai-master-api/internal/transport/http/handlers/musics"
	systemhandlers "sekai-master-api/internal/transport/http/handlers/system"
	virtuallivehandlers "sekai-master-api/internal/transport/http/handlers/virtuallives"
	"sekai-master-api/internal/transport/http/middleware"
	"sekai-master-api/internal/usecase"
)

func NewRouter(cfg config.Config, db *sql.DB, tokenVerifier auth.TokenVerifier, masterDataSync *usecase.MasterDataSyncUsecase, masterDataEvents *usecase.MasterDataEventHub, startupState *startup.State) (*gin.Engine, error) {
	router := gin.New()

	httpMetrics, err := middleware.HTTPMetrics()
	if err != nil {
		return nil, err
	}

	router.Use(middleware.RequestID())
	router.Use(otelgin.Middleware(cfg.OTELServiceName, otelgin.WithFilter(func(request *http.Request) bool {
		if request == nil || request.URL == nil {
			return false
		}
		return !strings.HasPrefix(request.URL.Path, "/docs")
	})))
	router.Use(httpMetrics)
	router.Use(middleware.AccessLog())
	router.Use(middleware.StartupGate(startupState))
	router.Use(middleware.RecoveryLog())

	healthHandler := systemhandlers.NewHealthHandler(db)
	versionsHandler := systemhandlers.NewVersionsHandler(masterDataSync)
	adminClaimAuthorizer := auth.NewAdminClaimAuthorizer(cfg.OIDCAdminClaim, cfg.OIDCAdminClaimValues)
	profileHandler := adminhandlers.NewProfileHandler(cfg.AppEnv, adminClaimAuthorizer)
	adminUIHandler := adminhandlers.NewAdminUIHandler(cfg)
	adminLoginHandler, err := adminhandlers.NewAdminLoginHandler(cfg)
	if err != nil {
		return nil, err
	}
	masterDataEventHandler := adminhandlers.NewMasterDataEventHandler(masterDataEvents)
	gitHubWebhookHandler := systemhandlers.NewGitHubWebhookHandler(
		cfg.MasterDataSources,
		masterDataSync,
		time.Duration(cfg.MasterDataSyncTimeout)*time.Second,
		cfg.MasterDataGitHubWebhookSecret,
	)
	cardHandler := cardhandlers.NewCardHandler(masterDataSync)
	musicHandler := musichandlers.NewMusicHandler(masterDataSync)
	eventHandler := eventhandlers.NewEventHandler(masterDataSync)
	lookupHandler := lookuphandlers.NewLookupHandler(masterDataSync)
	virtualLiveHandler := virtuallivehandlers.NewVirtualLiveHandler(masterDataSync)
	masterDataAdminHandler := adminhandlers.NewMasterDataAdminHandler(masterDataSync, startupState)

	if isSwaggerEnabledEnv(cfg.AppEnv) {
		router.GET("/docs/*any", swaggerHandler())
	}

	v1 := router.Group("/api/v1")
	registerPublicRoutes(v1, healthHandler, versionsHandler, cardHandler, musicHandler, eventHandler, lookupHandler, virtualLiveHandler)
	registerInternalRoutes(v1, gitHubWebhookHandler)

	registerAdminRoutes(
		router,
		v1,
		tokenVerifier,
		adminClaimAuthorizer,
		adminUIHandler,
		adminLoginHandler,
		profileHandler,
		masterDataEventHandler,
		masterDataAdminHandler,
	)

	return router, nil
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
