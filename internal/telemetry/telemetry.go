// Package telemetry wires OpenTelemetry traces and metrics to an OTLP endpoint.
// It is enabled only when OTEL_EXPORTER_OTLP_ENDPOINT is set; all standard
// OTEL_* environment variables (service name, headers, resource attributes) apply.
package telemetry

import (
	"context"
	"errors"
	"net/http"
	"os"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Enabled reports whether an OTLP endpoint is configured.
func Enabled() bool { return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" }

// Setup installs global tracer and meter providers. The returned function
// flushes and shuts them down. When telemetry is disabled it is a no-op.
func Setup(ctx context.Context, defaultService string) (func(context.Context) error, error) {
	if !Enabled() {
		return func(context.Context) error { return nil }, nil
	}
	if os.Getenv("OTEL_SERVICE_NAME") == "" {
		os.Setenv("OTEL_SERVICE_NAME", defaultService)
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL))
	if err != nil {
		return nil, err
	}
	res, err = resource.New(ctx, resource.WithFromEnv(), resource.WithTelemetrySDK(), resource.WithHost(), resource.WithAttributes(res.Attributes()...))
	if err != nil {
		return nil, err
	}
	te, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	me, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(te), sdktrace.WithResource(res))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(me)), sdkmetric.WithResource(res))
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return func(ctx context.Context) error { return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx)) }, nil
}

// HTTP wraps a handler so each request becomes a span with standard HTTP metrics.
func HTTP(h http.Handler, operation string) http.Handler {
	if !Enabled() {
		return h
	}
	return otelhttp.NewHandler(h, operation, otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
		return r.Method + " " + r.URL.Path
	}))
}
