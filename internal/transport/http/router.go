package http

import (
	"database/sql"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/auth"
	"sekai-master-api/internal/config"
	"sekai-master-api/internal/transport/http/handler"
	"sekai-master-api/internal/transport/http/middleware"
	"sekai-master-api/internal/usecase"
)

func NewRouter(cfg config.Config, db *sql.DB, tokenVerifier auth.TokenVerifier, masterDataSync *usecase.MasterDataSyncUsecase) *gin.Engine {
	router := gin.New()
	router.Use(gin.Logger())
	router.Use(gin.Recovery())
	router.Use(middleware.RequestID())

	healthHandler := handler.NewHealthHandler(db)
	profileHandler := handler.NewProfileHandler()
	adminUIHandler := handler.NewAdminUIHandler(cfg)
	adminLoginHandler := handler.NewAdminLoginHandler(cfg)
	masterDataStatusHandler := handler.NewMasterDataStatusHandler(masterDataSync)
	masterDataAdminHandler := handler.NewMasterDataAdminHandler(masterDataSync, time.Duration(cfg.MasterDataSyncTimeout)*time.Second)

	router.GET("/admin/login", adminUIHandler.LoginPage)
	router.GET("/admin", adminUIHandler.DashboardPage)
	router.GET("/admin/assets/*filepath", adminUIHandler.Asset)

	v1 := router.Group("/api/v1")
	{
		v1.GET("/health", healthHandler.Check)
		v1.GET("/master-data/status", masterDataStatusHandler.List)
		v1.POST("/admin/login", adminLoginHandler.Login)

		admin := v1.Group("/admin")
		admin.Use(middleware.Auth(tokenVerifier))
		admin.GET("/profile", profileHandler.Me)
		admin.POST("/master-data/sync", masterDataAdminHandler.Sync)
	}

	return router
}
