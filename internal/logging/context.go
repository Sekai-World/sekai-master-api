package logging

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

func FromContext(ctx context.Context) *zap.SugaredLogger {
	logger := zap.S()
	if fields := TraceFields(ctx); len(fields) > 0 {
		return logger.With(fields...)
	}
	return logger
}

func TraceFields(ctx context.Context) []any {
	if ctx == nil {
		return nil
	}

	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return nil
	}

	return []any{
		"trace_id", spanContext.TraceID().String(),
		"span_id", spanContext.SpanID().String(),
	}
}

func DetachedTraceContext(ctx context.Context) context.Context {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return context.Background()
	}
	return trace.ContextWithSpanContext(context.Background(), spanContext)
}
