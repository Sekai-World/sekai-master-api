package http

import (
	"github.com/gin-gonic/gin"

	systemhandlers "sekai-master-api/internal/transport/http/handlers/system"
)

func registerInternalRoutes(
	v1 *gin.RouterGroup,
	gitHubWebhookHandler *systemhandlers.GitHubWebhookHandler,
) {
	if gitHubWebhookHandler == nil {
		return
	}

	internal := v1.Group("/internal")
	internal.POST("/github/webhooks/master-data", gitHubWebhookHandler.MasterData)
}
