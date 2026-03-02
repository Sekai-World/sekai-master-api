package response

import (
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func JSON(c *gin.Context, status int, payload any) {
	c.JSON(status, payload)
}

func Error(c *gin.Context, status int, code string, message string) {
	requestID := strings.TrimSpace(c.GetString("request_id"))
	if requestID == "" {
		requestID = "missing"
	}

	zap.S().Warnw(
		"request failed",
		"request_id", requestID,
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"status", status,
		"error_code", code,
		"error_message", message,
	)

	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
}
