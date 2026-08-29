package http

import (
	"context"

	"github.com/gin-gonic/gin"

	systemhandlers "sekai-master-api/internal/transport/http/handlers/system"
)

func registerInternalRoutes(
	v1 *gin.RouterGroup,
	gitHubWebhookHandler *systemhandlers.GitHubWebhookHandler,
	lifecycleCtx context.Context,
) {
	if gitHubWebhookHandler == nil {
		return
	}

	internal := v1.Group("/internal")
	internal.POST("/github/webhooks/master-data", func(c *gin.Context) {
		gitHubWebhookHandler.MasterData(c, lifecycleCtx)
	})
}
