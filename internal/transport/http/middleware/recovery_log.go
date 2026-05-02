package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"sekai-master-api/internal/logging"
)

func RecoveryLog() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		logging.FromContext(c.Request.Context()).Errorw(
			"panic recovered",
			"component", "gin-recovery",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"panic", fmt.Sprintf("%v", recovered),
			"stack", string(debug.Stack()),
		)
		c.AbortWithStatus(http.StatusInternalServerError)
	})
}
