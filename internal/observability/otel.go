package observability

import (
	"context"
	"errors"
	"log"
	"runtime/debug"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"sekai-master-api/internal/config"
)

const shutdownTimeout = 5 * time.Second

func Setup(ctx context.Context, cfg config.Config) (func(), error) {
	if !cfg.OTELEnabled {
		return func() {}, nil
	}

	res, err := newResource(ctx, cfg)
	if err != nil {
		return nil, err
	}

	traceExporter, err := newTraceExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}

	metricExporter, err := newMetricExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}

	readerOptions := make([]metric.PeriodicReaderOption, 0, 1)
	if cfg.OTELMetricExportIntervalMS > 0 {
		readerOptions = append(readerOptions, metric.WithInterval(time.Duration(cfg.OTELMetricExportIntervalMS)*time.Millisecond))
	}

	meterProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(metricExporter, readerOptions...)),
	)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithBatcher(traceExporter),
	)

	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	runtimeOptions := make([]runtime.Option, 0, 2)
	runtimeOptions = append(runtimeOptions, runtime.WithMeterProvider(meterProvider))
	if cfg.OTELMetricExportIntervalMS > 0 {
		runtimeOptions = append(
			runtimeOptions,
			runtime.WithMinimumReadMemStatsInterval(time.Duration(cfg.OTELMetricExportIntervalMS)*time.Millisecond),
		)
	}

	if err := runtime.Start(runtimeOptions...); err != nil {
		shutdownProviders(meterProvider, tracerProvider)
		return nil, err
	}

	return func() {
		shutdownProviders(meterProvider, tracerProvider)
	}, nil
}

func newResource(ctx context.Context, cfg config.Config) (*resource.Resource, error) {
	attributes := []attribute.KeyValue{
		attribute.String("service.name", defaultString(cfg.OTELServiceName, "sekai-master-api")),
		attribute.String("deployment.environment", strings.TrimSpace(cfg.AppEnv)),
	}

	if version := resolveServiceVersion(cfg.OTELServiceVersion); version != "" {
		attributes = append(attributes, attribute.String("service.version", version))
	}

	res, err := resource.New(
		ctx,
		resource.WithFromEnv(),
		resource.WithHost(),
		resource.WithOS(),
		resource.WithProcess(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(attributes...),
	)
	if err != nil && !errors.Is(err, resource.ErrPartialResource) {
		return nil, err
	}

	return res, nil
}

func newTraceExporter(ctx context.Context, cfg config.Config) (sdktrace.SpanExporter, error) {
	options := make([]otlptracehttp.Option, 0, 2)
	if endpoint := strings.TrimSpace(cfg.OTELExporterOTLPEndpoint); endpoint != "" {
		options = append(options, otlptracehttp.WithEndpointURL(endpoint))
	}
	if cfg.OTELExporterOTLPInsecure {
		options = append(options, otlptracehttp.WithInsecure())
	}

	return otlptracehttp.New(ctx, options...)
}

func newMetricExporter(ctx context.Context, cfg config.Config) (*otlpmetrichttp.Exporter, error) {
	options := make([]otlpmetrichttp.Option, 0, 2)
	if endpoint := strings.TrimSpace(cfg.OTELExporterOTLPEndpoint); endpoint != "" {
		options = append(options, otlpmetrichttp.WithEndpointURL(endpoint))
	}
	if cfg.OTELExporterOTLPInsecure {
		options = append(options, otlpmetrichttp.WithInsecure())
	}

	return otlpmetrichttp.New(ctx, options...)
}

func shutdownProviders(meterProvider *metric.MeterProvider, tracerProvider *sdktrace.TracerProvider) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var shutdownErr error
	if meterProvider != nil {
		shutdownErr = errors.Join(shutdownErr, meterProvider.Shutdown(ctx))
	}
	if tracerProvider != nil {
		shutdownErr = errors.Join(shutdownErr, tracerProvider.Shutdown(ctx))
	}
	if shutdownErr != nil {
		log.Printf("otel shutdown failed: %v", shutdownErr)
	}
}

func resolveServiceVersion(explicitVersion string) string {
	if version := strings.TrimSpace(explicitVersion); version != "" {
		return version
	}

	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}

	version := strings.TrimSpace(buildInfo.Main.Version)
	if version == "" || version == "(devel)" {
		return ""
	}

	return version
}

func defaultString(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}

	return trimmed
}
