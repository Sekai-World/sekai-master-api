package http

import (
	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/auth"
	adminhandlers "sekai-master-api/internal/transport/http/handlers/admin"
	"sekai-master-api/internal/transport/http/middleware"
)

func registerAdminRoutes(
	router *gin.Engine,
	v1 *gin.RouterGroup,
	tokenVerifier auth.TokenVerifier,
	adminClaimAuthorizer *auth.AdminClaimAuthorizer,
	adminUIHandler *adminhandlers.AdminUIHandler,
	adminLoginHandler *adminhandlers.AdminLoginHandler,
	profileHandler *adminhandlers.ProfileHandler,
	masterDataEventHandler *adminhandlers.MasterDataEventHandler,
	masterDataAdminHandler *adminhandlers.MasterDataAdminHandler,
) {
	router.GET("/admin/login", adminUIHandler.LoginPage)
	router.GET("/admin", adminUIHandler.DashboardPage)
	router.GET("/admin/assets/*filepath", adminUIHandler.Asset)

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
