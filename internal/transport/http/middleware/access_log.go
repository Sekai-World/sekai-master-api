package middleware

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		statusCode := c.Writer.Status()
		latency := time.Since(start)
		requestPath := c.Request.URL.Path
		if rawQuery := strings.TrimSpace(c.Request.URL.RawQuery); rawQuery != "" {
			requestPath = requestPath + "?" + rawQuery
		}

		requestID := strings.TrimSpace(c.GetString("request_id"))
		if requestID == "" {
			requestID = "missing"
		}

		fields := []any{
			"component", "gin-access",
			"request_id", requestID,
			"method", c.Request.Method,
			"path", requestPath,
			"status", statusCode,
			"latency_ms", latency.Milliseconds(),
			"client_ip", c.ClientIP(),
			"user_agent", c.Request.UserAgent(),
			"bytes_written", c.Writer.Size(),
		}

		if len(c.Errors) > 0 {
			fields = append(fields, "errors", c.Errors.String())
		}

		switch {
		case statusCode >= 500:
			zap.S().Errorw("http request", fields...)
		case statusCode >= 400:
			zap.S().Warnw("http request", fields...)
		default:
			zap.S().Infow("http request", fields...)
		}
	}
}
