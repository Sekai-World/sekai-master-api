package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	apimetric "go.opentelemetry.io/otel/metric"
)

type httpMetrics struct {
	requestsTotal   apimetric.Int64Counter
	requestDuration apimetric.Float64Histogram
	activeRequests  apimetric.Int64UpDownCounter
}

func HTTPMetrics() (gin.HandlerFunc, error) {
	meter := otel.Meter("sekai-master-api/http")

	requestsTotal, err := meter.Int64Counter(
		"sekai_http_server_requests_total",
		apimetric.WithDescription("Total HTTP requests handled by the API."),
	)
	if err != nil {
		return nil, err
	}

	requestDuration, err := meter.Float64Histogram(
		"sekai_http_server_request_duration_ms",
		apimetric.WithDescription("End-to-end HTTP request duration in milliseconds."),
		apimetric.WithUnit("ms"),
		apimetric.WithExplicitBucketBoundaries(5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000),
	)
	if err != nil {
		return nil, err
	}

	activeRequests, err := meter.Int64UpDownCounter(
		"sekai_http_server_active_requests",
		apimetric.WithDescription("Current in-flight HTTP requests."),
	)
	if err != nil {
		return nil, err
	}

	instruments := httpMetrics{
		requestsTotal:   requestsTotal,
		requestDuration: requestDuration,
		activeRequests:  activeRequests,
	}

	return func(c *gin.Context) {
		start := time.Now()
		instruments.activeRequests.Add(c.Request.Context(), 1)
		defer instruments.activeRequests.Add(c.Request.Context(), -1)

		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}

		attributes := []attribute.KeyValue{
			attribute.String("http.method", c.Request.Method),
			attribute.String("http.route", route),
			attribute.Int("http.status_code", c.Writer.Status()),
		}

		durationMS := float64(time.Since(start)) / float64(time.Millisecond)
		instruments.requestsTotal.Add(c.Request.Context(), 1, apimetric.WithAttributes(attributes...))
		instruments.requestDuration.Record(c.Request.Context(), durationMS, apimetric.WithAttributes(attributes...))
	}, nil
}
