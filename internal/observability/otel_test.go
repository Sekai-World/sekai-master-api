package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"sekai-master-api/internal/config"
)

func TestDefaultTraceSampler(t *testing.T) {
	tests := []struct {
		name        string
		appEnv      string
		description string
	}{
		{name: "development", appEnv: "development", description: "AlwaysOnSampler"},
		{name: "test", appEnv: "test", description: "AlwaysOnSampler"},
		{name: "production", appEnv: "production", description: "TraceIDRatioBased"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sampler := defaultTraceSampler(tt.appEnv)
			if !strings.Contains(sampler.Description(), tt.description) {
				t.Fatalf("expected sampler description to contain %q, got %q", tt.description, sampler.Description())
			}
		})
	}
}

func TestSetupKeepsExistingTracerProviderWhenTracingDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	originalProvider := otel.GetTracerProvider()
	testProvider := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(testProvider)
	t.Cleanup(func() {
		otel.SetTracerProvider(originalProvider)
		_ = testProvider.Shutdown(context.Background())
	})

	shutdown, err := Setup(context.Background(), config.Config{
		AppEnv:                     "test",
		OTELEnabled:                true,
		OTELTracingEnabled:         false,
		OTELServiceName:            "sekai-master-api-test",
		OTELExporterOTLPEndpoint:   server.URL,
		OTELExporterOTLPInsecure:   true,
		OTELMetricExportIntervalMS: 60_000,
	})
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	defer shutdown()

	if got := otel.GetTracerProvider(); got != testProvider {
		t.Fatalf("expected tracing-disabled setup to preserve the existing tracer provider")
	}
}
