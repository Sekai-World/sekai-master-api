package http

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	_ "sekai-master-api/docs"

	"sekai-master-api/internal/auth"
	"sekai-master-api/internal/config"
	"sekai-master-api/internal/startup"
	"sekai-master-api/internal/transport/http/handler"
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

	healthHandler := handler.NewHealthHandler(db)
	adminClaimAuthorizer := auth.NewAdminClaimAuthorizer(cfg.OIDCAdminClaim, cfg.OIDCAdminClaimValues)
	profileHandler := handler.NewProfileHandler(cfg.AppEnv, adminClaimAuthorizer)
	adminUIHandler := handler.NewAdminUIHandler(cfg)
	adminLoginHandler, err := handler.NewAdminLoginHandler(cfg)
	if err != nil {
		return nil, err
	}
	masterDataEventHandler := handler.NewMasterDataEventHandler(masterDataEvents)
	cardHandler := handler.NewCardHandler(masterDataSync)
	musicHandler := handler.NewMusicHandler(masterDataSync)
	eventHandler := handler.NewEventHandler(masterDataSync)
	virtualLiveHandler := handler.NewVirtualLiveHandler(masterDataSync)
	masterDataAdminHandler := handler.NewMasterDataAdminHandler(masterDataSync, startupState)

	router.GET("/admin/login", adminUIHandler.LoginPage)
	router.GET("/admin", adminUIHandler.DashboardPage)
	router.GET("/admin/assets/*filepath", adminUIHandler.Asset)
	if isSwaggerEnabledEnv(cfg.AppEnv) {
		router.GET("/docs/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}

	v1 := router.Group("/api/v1")
	{
		v1.GET("/health", healthHandler.Check)
		v1.GET("/cards/regions/:id/availability", cardHandler.AvailableRegionsByID)
		v1.GET("/cards/:region/list", cardHandler.List)
		v1.GET("/cards/:region/search", cardHandler.SearchByPrefix)
		v1.GET("/cards/:region/:id", cardHandler.ByID)
		v1.GET("/cards/:region/:id/params", cardHandler.ParamsByID)
		v1.GET("/cards/:region/:id/episodes", cardHandler.EpisodesByID)
		v1.GET("/musics/regions/:id/availability", musicHandler.AvailableRegionsByID)
		v1.GET("/musics/:region/list", musicHandler.List)
		v1.GET("/musics/:region/search", musicHandler.Search)
		v1.GET("/musics/:region/:id", musicHandler.ByID)
		v1.GET("/events/regions/:id/availability", eventHandler.AvailableRegionsByID)
		v1.GET("/events/:region/current", eventHandler.Current)
		v1.GET("/events/:region/list", eventHandler.List)
		v1.GET("/events/:region/search", eventHandler.Search)
		v1.GET("/events/:region/:id", eventHandler.ByID)
		v1.GET("/events/:region/:id/break-times", eventHandler.BreakTimesByID)
		v1.GET("/events/:region/:id/bonuses", eventHandler.BonusesByID)
		v1.GET("/events/:region/:id/cards", eventHandler.CardsByID)
		v1.GET("/events/:region/:id/musics", eventHandler.MusicsByID)
		v1.GET("/events/:region/:id/rewards", eventHandler.RewardsByID)
		v1.GET("/virtualLives/regions/:id/availability", virtualLiveHandler.AvailableRegionsByID)
		v1.GET("/virtualLives/:region/list", virtualLiveHandler.List)
		v1.GET("/virtualLives/:region/search", virtualLiveHandler.Search)
		v1.GET("/virtualLives/:region/:id/items", virtualLiveHandler.ItemsByID)
		v1.GET("/virtualLives/:region/:id/schedules", virtualLiveHandler.SchedulesByID)
		v1.GET("/virtualLives/:region/:id/setlists", virtualLiveHandler.SetlistsByID)
		v1.GET("/virtualLives/:region/:id", virtualLiveHandler.ByID)
		v1.GET("/admin/login", adminLoginHandler.Start)
		v1.GET("/admin/login/callback", adminLoginHandler.Callback)

		admin := v1.Group("/admin")
		admin.Use(middleware.AuthWithAuthorizer(tokenVerifier, adminClaimAuthorizer))
		admin.GET("/profile", profileHandler.Me)
		admin.GET("/master-data/events", masterDataEventHandler.Stream)
		admin.GET("/master-data/status", masterDataAdminHandler.Status)
		admin.POST("/master-data/sync", masterDataAdminHandler.Sync)
		admin.POST("/master-data/sync/force", masterDataAdminHandler.ForceSync)
	}

	return router, nil
}

func isSwaggerEnabledEnv(appEnv string) bool {
	normalized := strings.ToLower(strings.TrimSpace(appEnv))
	return normalized == "development" || normalized == "dev" || normalized == "test"
}
