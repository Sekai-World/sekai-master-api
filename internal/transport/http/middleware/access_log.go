package middleware

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"sekai-master-api/internal/logging"
)

func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		requestPath := c.Request.URL.Path
		requestQuery := sanitizeQueryString(c.Request.URL.RawQuery)
		requestPathWithQuery := requestPath
		if requestQuery != "" {
			requestPathWithQuery = requestPathWithQuery + "?" + requestQuery
		}

		requestID := strings.TrimSpace(c.GetString("request_id"))
		if requestID == "" {
			requestID = "missing"
		}
		logger := logging.FromContext(c.Request.Context())

		logger.Debugw(
			"http request",
			"component", "gin-access",
			"request_id", requestID,
			"request_method", c.Request.Method,
			"request_path", requestPath,
			"request_query", requestQuery,
			"request_path_with_query", requestPathWithQuery,
			"request_route", c.FullPath(),
			"request_proto", c.Request.Proto,
			"client_ip", c.ClientIP(),
			"user_agent", c.Request.UserAgent(),
		)

		c.Next()

		statusCode := c.Writer.Status()
		latency := time.Since(start)

		responseContentType := strings.TrimSpace(c.Writer.Header().Get("Content-Type"))
		if responseContentType == "" {
			responseContentType = "unknown"
		}

		responseStatusText := strings.TrimSpace(http.StatusText(statusCode))
		if responseStatusText == "" {
			responseStatusText = "unknown"
		}

		fields := []any{
			"component", "gin-access",
			"request_id", requestID,
			"latency_ms", latency.Milliseconds(),
			"response_status", statusCode,
			"response_status_text", responseStatusText,
			"response_content_type", responseContentType,
			"response_bytes", c.Writer.Size(),
		}

		if len(c.Errors) > 0 {
			fields = append(fields, "errors", c.Errors.String())
		}

		logger.Debugw("http response", fields...)
	}
}

func sanitizeQueryString(rawQuery string) string {
	trimmed := strings.TrimSpace(rawQuery)
	if trimmed == "" {
		return ""
	}

	values, err := url.ParseQuery(trimmed)
	if err != nil {
		return trimmed
	}

	if _, ok := values["access_token"]; ok {
		values.Set("access_token", "[REDACTED]")
	}

	return values.Encode()
}
