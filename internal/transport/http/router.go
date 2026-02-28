package http

import (
	"database/sql"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/auth"
	"sekai-master-api/internal/config"
	"sekai-master-api/internal/transport/http/handler"
	"sekai-master-api/internal/transport/http/middleware"
)

func NewRouter(cfg config.Config, db *sql.DB, tokenVerifier auth.TokenVerifier) *gin.Engine {
	router := gin.New()
	router.Use(gin.Logger())
	router.Use(gin.Recovery())
	router.Use(middleware.RequestID())

	healthHandler := handler.NewHealthHandler(db)
	profileHandler := handler.NewProfileHandler()
	adminUIHandler := handler.NewAdminUIHandler()
	adminLoginHandler := handler.NewAdminLoginHandler(cfg)

	router.GET("/admin/login", adminUIHandler.LoginPage)
	router.GET("/admin", adminUIHandler.DashboardPage)
	router.GET("/admin/assets/*filepath", adminUIHandler.Asset)

	v1 := router.Group("/api/v1")
	{
		v1.GET("/health", healthHandler.Check)
		v1.POST("/admin/login", adminLoginHandler.Login)

		admin := v1.Group("/admin")
		admin.Use(middleware.Auth(tokenVerifier))
		admin.GET("/profile", profileHandler.Me)
	}

	return router
}
