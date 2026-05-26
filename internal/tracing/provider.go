package tracing

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
)

// Provider wraps an OpenTelemetry TracerProvider.
// When OTLP is disabled, it wraps the no-op provider so callers never need
// to nil-check spans.
type Provider struct {
	tp     *sdktrace.TracerProvider // nil when no-op
	tracer trace.Tracer
}

// New creates a Provider. If cfg.Enabled is false, a no-op provider is returned
// and no network connections are made. Callers can always call Middleware and Shutdown
// safely regardless of the enabled state.
func New(cfg config.OTLPConfig, serviceName string) (*Provider, error) {
	if !cfg.Enabled {
		noop := tracenoop.NewTracerProvider()
		otel.SetTracerProvider(noop)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
		return &Provider{tracer: noop.Tracer(serviceName)}, nil
	}

	opts := []grpc.DialOption{}
	if cfg.Insecure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	// grpc.NewClient is the modern, non-blocking constructor.
	conn, err := grpc.NewClient(cfg.Endpoint, opts...)
	if err != nil {
		return nil, fmt.Errorf("tracing: dial OTLP endpoint %q: %w", cfg.Endpoint, err)
	}

	ctx := context.Background()
	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("tracing: create OTLP exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("tracing: create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return &Provider{tp: tp, tracer: tp.Tracer(serviceName)}, nil
}

// Middleware returns an HTTP middleware that creates a span for every request.
// The span name uses the chi route pattern to avoid high-cardinality names from
// URL path parameters (e.g. /mcp/{serverName}/message, not /mcp/my-server/message).
func (p *Provider) Middleware() func(http.Handler) http.Handler {
	return otelhttp.NewMiddleware("mcpgate",
		otelhttp.WithTracerProvider(otel.GetTracerProvider()),
		otelhttp.WithPropagators(otel.GetTextMapPropagator()),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.RoutePattern() != "" {
				return r.Method + " " + rctx.RoutePattern()
			}
			return r.Method + " " + r.URL.Path
		}),
	)
}

// Shutdown flushes any pending spans. Safe to call on a no-op Provider.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p.tp == nil {
		return nil
	}
	return p.tp.Shutdown(ctx)
}
