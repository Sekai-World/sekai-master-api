package response

import (
	"log"
	"strings"

	"github.com/gin-gonic/gin"
)

func JSON(c *gin.Context, status int, payload any) {
	c.JSON(status, payload)
}

func Error(c *gin.Context, status int, code string, message string) {
	requestID := strings.TrimSpace(c.GetString("request_id"))
	if requestID == "" {
		requestID = "missing"
	}

	log.Printf(
		"request_id=%s method=%s path=%s status=%d error_code=%s error_message=%s",
		requestID,
		c.Request.Method,
		c.Request.URL.Path,
		status,
		code,
		message,
	)

	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
}
