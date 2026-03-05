package http

import (
	"database/sql"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	_ "sekai-master-api/docs"

	"sekai-master-api/internal/auth"
	"sekai-master-api/internal/config"
	"sekai-master-api/internal/transport/http/handler"
	"sekai-master-api/internal/transport/http/middleware"
	"sekai-master-api/internal/usecase"
)

func NewRouter(cfg config.Config, db *sql.DB, tokenVerifier auth.TokenVerifier, masterDataSync *usecase.MasterDataSyncUsecase, masterDataEvents *usecase.MasterDataEventHub) *gin.Engine {
	router := gin.New()
	router.Use(middleware.RequestID())
	router.Use(middleware.AccessLog())
	router.Use(middleware.RecoveryLog())

	healthHandler := handler.NewHealthHandler(db)
	profileHandler := handler.NewProfileHandler()
	adminUIHandler := handler.NewAdminUIHandler(cfg)
	adminLoginHandler := handler.NewAdminLoginHandler(cfg)
	masterDataStatusHandler := handler.NewMasterDataStatusHandler(masterDataSync)
	masterDataEventHandler := handler.NewMasterDataEventHandler(masterDataEvents)
	cardHandler := handler.NewCardHandler(masterDataSync)
	musicHandler := handler.NewMusicHandler(masterDataSync)
	eventHandler := handler.NewEventHandler(masterDataSync)
	masterDataAdminHandler := handler.NewMasterDataAdminHandler(masterDataSync, time.Duration(cfg.MasterDataSyncTimeout)*time.Second)

	router.GET("/admin/login", adminUIHandler.LoginPage)
	router.GET("/admin", adminUIHandler.DashboardPage)
	router.GET("/admin/assets/*filepath", adminUIHandler.Asset)
	if isSwaggerEnabledEnv(cfg.AppEnv) {
		router.GET("/docs/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}

	v1 := router.Group("/api/v1")
	{
		v1.GET("/health", healthHandler.Check)
		v1.GET("/master-data/status", masterDataStatusHandler.List)
		v1.GET("/master-data/events", masterDataEventHandler.Stream)
		v1.GET("/cards/:region/list", cardHandler.List)
		v1.GET("/cards/:region/search", cardHandler.SearchByPrefix)
		v1.GET("/cards/:region/:id", cardHandler.ByID)
		v1.GET("/cards/:region/:id/params", cardHandler.ParamsByID)
		v1.GET("/cards/:region/:id/episodes", cardHandler.EpisodesByID)
		v1.GET("/musics/:region/list", musicHandler.List)
		v1.GET("/musics/:region/search", musicHandler.Search)
		v1.GET("/musics/:region/:id", musicHandler.ByID)
		v1.GET("/events/:region/current", eventHandler.Current)
		v1.GET("/events/:region/:id", eventHandler.ByID)
		v1.GET("/events/:region/:id/rewards", eventHandler.RewardsByID)
		v1.POST("/admin/login", adminLoginHandler.Login)

		admin := v1.Group("/admin")
		admin.Use(middleware.Auth(tokenVerifier))
		admin.GET("/profile", profileHandler.Me)
		admin.POST("/master-data/sync", masterDataAdminHandler.Sync)
		admin.POST("/master-data/sync/force", masterDataAdminHandler.ForceSync)
	}

	return router
}

func isSwaggerEnabledEnv(appEnv string) bool {
	normalized := strings.ToLower(strings.TrimSpace(appEnv))
	return normalized == "development" || normalized == "dev" || normalized == "test"
}
