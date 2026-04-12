package observability

import (
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func NewHTTPTransport(base http.RoundTripper, component string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}

	options := make([]otelhttp.Option, 0, 1)
	component = strings.TrimSpace(component)
	if component != "" {
		options = append(options, otelhttp.WithSpanNameFormatter(func(_ string, request *http.Request) string {
			if request == nil {
				return component
			}
			return component + " " + request.Method
		}))
	}

	return otelhttp.NewTransport(base, options...)
}
