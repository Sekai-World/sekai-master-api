package observability

import (
	"strings"
	"testing"
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
