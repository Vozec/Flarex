// Package tracing bootstraps an OTLP tracer exporter. When Endpoint is empty
// the tracer is a noop — zero overhead. Otherwise span data is pushed to the
// OTLP/gRPC collector at Endpoint (e.g. "localhost:4317").
package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const ServiceName = "flarex"

var tracer trace.Tracer = otel.Tracer(ServiceName)

// Init sets up the global tracer provider with OTLP/gRPC exporter.
// endpoint format: "host:port" (no scheme). insecure=true uses plaintext gRPC.
// Returns a shutdown func that flushes spans; call on process exit.
func Init(ctx context.Context, endpoint string, insecure bool) (func(context.Context) error, error) {
	if endpoint == "" {
		tracer = otel.Tracer(ServiceName)
		return func(context.Context) error { return nil }, nil
	}
	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(endpoint)}
	if insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	exp, err := otlptrace.New(ctx, otlptracegrpc.NewClient(opts...))
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}
	res, _ := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes("", semconv.ServiceName(ServiceName)),
	)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	tracer = tp.Tracer(ServiceName)
	return tp.Shutdown, nil
}

// StartDial opens a span for an outbound dial. Typical attrs on end:
// worker, mode, target_host, retry_count.
func StartDial(ctx context.Context, host string, port int) (context.Context, trace.Span) {
	return tracer.Start(ctx, "proxy.dial",
		trace.WithAttributes(
			attribute.String("target.host", host),
			attribute.Int("target.port", port),
		),
	)
}

// Attr shortcuts.
func Str(k, v string) attribute.KeyValue     { return attribute.String(k, v) }
func Int(k string, v int) attribute.KeyValue { return attribute.Int(k, v) }
