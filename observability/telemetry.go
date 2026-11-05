// Package telemetry is the single OpenTelemetry bootstrap shared by every
// service in this repository (api-gateway, auth, product, order, payment and
// the notification worker).
//
// It provides:
//
//   - Init / Shutdown — OTLP exporters (traces, metrics, logs), resource
//     attributes, samplers, periodic readers and graceful flush on shutdown.
//   - Structured logging (log/slog) correlated with trace_id / span_id.
//   - HTTP (Gin + reverse proxy), gRPC, PostgreSQL (GORM), Redis and
//     RabbitMQ instrumentation with W3C trace-context propagation.
//
// Every service only has to call:
//
//	tel, err := telemetry.Init(ctx, telemetry.OptionsFromEnv("my-service", "1.0.0"))
//	defer tel.Shutdown(ctx)
//	telemetry.InitLogger("my-service")
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	// DefaultEndpoint is the OTLP/HTTP endpoint exposed by the OpenTelemetry
	// Collector defined in infrastructure/observability/docker-compose.yml.
	DefaultEndpoint = "http://localhost:4318"

	// DefaultProtocol is OTLP over HTTP/protobuf (port 4318).
	DefaultProtocol = "http/protobuf"

	// ShutdownTimeout bounds how long providers may take to flush pending
	// telemetry when a service is stopping.
	ShutdownTimeout = 10 * time.Second
)

// Options configures telemetry for a single service.
type Options struct {
	// ServiceName is reported as resource attribute service.name.
	// OTEL_SERVICE_NAME overrides it when set.
	ServiceName string
	// ServiceVersion is reported as service.version.
	ServiceVersion string
	// Environment is reported as deployment.environment (default "local").
	Environment string

	// Endpoint is the OTLP collector endpoint, e.g. "http://localhost:4318".
	Endpoint string
	// Protocol is "http/protobuf" (default) or "grpc".
	Protocol string
	// Headers are extra headers sent with every OTLP request.
	Headers map[string]string

	// SampleRatio is the trace-id ratio sampler argument (0.0 - 1.0).
	SampleRatio float64
	// MetricInterval is how often periodic metrics are exported.
	MetricInterval time.Duration

	// Enabled=false turns telemetry into a no-op (still safe to Shutdown).
	Enabled bool
}

// OptionsFromEnv builds Options with the standard OpenTelemetry environment
// variables honoured (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_PROTOCOL,
// OTEL_SERVICE_NAME, OTEL_SDK_DISABLED, OTEL_TRACES_SAMPLER_ARG,
// OTEL_METRIC_EXPORT_INTERVAL, OTEL_DEPLOYMENT_ENVIRONMENT).
func OptionsFromEnv(serviceName, serviceVersion string) Options {
	opts := Options{
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
		Environment:    envOr("OTEL_DEPLOYMENT_ENVIRONMENT", "local"),
		Endpoint:       envOr("OTEL_EXPORTER_OTLP_ENDPOINT", DefaultEndpoint),
		Protocol:       envOr("OTEL_EXPORTER_OTLP_PROTOCOL", DefaultProtocol),
		SampleRatio:    envFloat("OTEL_TRACES_SAMPLER_ARG", 1.0),
		MetricInterval: envDuration("OTEL_METRIC_EXPORT_INTERVAL", 5*time.Second),
		Enabled:        true,
	}
	if v := os.Getenv("OTEL_SERVICE_NAME"); v != "" {
		opts.ServiceName = v
	}
	if s := os.Getenv("OTEL_SDK_DISABLED"); strings.EqualFold(s, "true") {
		opts.Enabled = false
	}
	return opts
}

// App owns the telemetry providers of one process. Call Shutdown exactly once,
// after the HTTP servers and consumers have stopped, so in-flight telemetry is
// flushed before the process exits.
type App struct {
	opts     Options
	enabled  bool
	tp       *sdktrace.TracerProvider
	mp       *sdkmetric.MeterProvider
	lp       *sdklog.LoggerProvider
	shutdown func(context.Context) error
}

// Init configures the global OpenTelemetry tracer / meter / logger providers
// and the global W3C propagator. It never fails hard: if the collector is not
// reachable the exporters buffer and retry in the background, so services can
// start before the observability stack.
func Init(ctx context.Context, opts Options) (*App, error) {
	if opts.ServiceName == "" {
		opts.ServiceName = "unknown-service"
	}
	if opts.Protocol == "" {
		opts.Protocol = DefaultProtocol
	}
	if opts.Endpoint == "" {
		opts.Endpoint = DefaultEndpoint
	}
	if opts.SampleRatio <= 0 || opts.SampleRatio > 1 {
		opts.SampleRatio = 1.0
	}
	if opts.MetricInterval <= 0 {
		opts.MetricInterval = 5 * time.Second
	}

	// W3C traceparent + baggage for every propagation point (HTTP, gRPC,
	// RabbitMQ headers).
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	app := &App{opts: opts}
	if !opts.Enabled {
		slog.Info("telemetry disabled (OTEL_SDK_DISABLED=true)")
		return app, nil
	}

	res, err := buildResource(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("build resource: %w", err)
	}

	// ── Traces ──────────────────────────────────────────────────────────────
	spanExporter, err := newTraceExporter(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("create trace exporter: %w", err)
	}
	app.tp = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(spanExporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(
			sdktrace.TraceIDRatioBased(opts.SampleRatio),
		)),
	)
	otel.SetTracerProvider(app.tp)

	// ── Metrics ─────────────────────────────────────────────────────────────
	metricExporter, err := newMetricExporter(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("create metric exporter: %w", err)
	}
	app.mp = sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			metricExporter,
			sdkmetric.WithInterval(opts.MetricInterval),
		)),
	)
	otel.SetMeterProvider(app.mp)

	// ── Logs (structured slog records exported over OTLP) ───────────────────
	logExporter, err := newLogExporter(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("create log exporter: %w", err)
	}
	app.lp = sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
	)
	otel.SetLoggerProvider(app.lp)

	app.enabled = true
	app.shutdown = func(sctx context.Context) error {
		var errs []error
		if app.lp != nil {
			errs = append(errs, app.lp.Shutdown(sctx))
		}
		if app.mp != nil {
			errs = append(errs, app.mp.Shutdown(sctx))
		}
		if app.tp != nil {
			errs = append(errs, app.tp.Shutdown(sctx))
		}
		return errors.Join(errs...)
	}

	slog.Info("telemetry initialized",
		"service", opts.ServiceName,
		"version", opts.ServiceVersion,
		"endpoint", opts.Endpoint,
		"protocol", opts.Protocol,
		"environment", opts.Environment,
	)

	return app, nil
}

// Shutdown flushes and stops every provider. It is safe to call multiple times
// and safe to call on a disabled App.
func (a *App) Shutdown(ctx context.Context) error {
	if a == nil || !a.enabled || a.shutdown == nil {
		return nil
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ShutdownTimeout)
		defer cancel()
	}
	err := a.shutdown(ctx)
	if err != nil {
		slog.Warn("telemetry shutdown reported errors", "error", err)
	} else {
		slog.Info("telemetry flushed and stopped")
	}
	return err
}

// Enabled reports whether exporters are active.
func (a *App) Enabled() bool { return a != nil && a.enabled }

// Meter returns the process-wide meter for custom instruments.
func Meter(name string) metric.Meter { return otel.GetMeterProvider().Meter(name) }

// ---------------------------------------------------------------------------
// resource
// ---------------------------------------------------------------------------

func buildResource(ctx context.Context, opts Options) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		attribute.String("service.name", opts.ServiceName),
		attribute.String("service.version", opts.ServiceVersion),
		attribute.String("deployment.environment", opts.Environment),
		attribute.String("service.namespace", "distributed-service"),
		attribute.String("telemetry.sdk.language", "go"),
	}

	detected, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithProcess(),
	)
	if err != nil {
		// Fall back to the explicit attributes only; never fail startup over
		// an optional detector.
		detected = resource.Empty()
	}

	explicit := resource.NewWithAttributes("", attrs...)
	merged, err := resource.Merge(explicit, detected)
	if err != nil {
		return explicit, nil
	}
	return merged, nil
}

// ---------------------------------------------------------------------------
// exporters
// ---------------------------------------------------------------------------

func newTraceExporter(ctx context.Context, opts Options) (sdktrace.SpanExporter, error) {
	if strings.EqualFold(opts.Protocol, "grpc") {
		return otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpointURL(opts.Endpoint),
			otlptracegrpc.WithHeaders(opts.Headers),
		)
	}
	return otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(signalEndpoint(opts.Endpoint, "/v1/traces")),
		otlptracehttp.WithHeaders(opts.Headers),
	)
}

func newMetricExporter(ctx context.Context, opts Options) (sdkmetric.Exporter, error) {
	if strings.EqualFold(opts.Protocol, "grpc") {
		return otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithEndpointURL(opts.Endpoint),
			otlpmetricgrpc.WithHeaders(opts.Headers),
		)
	}
	return otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(signalEndpoint(opts.Endpoint, "/v1/metrics")),
		otlpmetrichttp.WithHeaders(opts.Headers),
	)
}

func newLogExporter(ctx context.Context, opts Options) (sdklog.Exporter, error) {
	if strings.EqualFold(opts.Protocol, "grpc") {
		return otlploggrpc.New(ctx,
			otlploggrpc.WithEndpointURL(opts.Endpoint),
			otlploggrpc.WithHeaders(opts.Headers),
		)
	}
	return otlploghttp.New(ctx,
		otlploghttp.WithEndpointURL(signalEndpoint(opts.Endpoint, "/v1/logs")),
		otlploghttp.WithHeaders(opts.Headers),
	)
}

// signalEndpoint appends the OTLP HTTP signal path to a base endpoint URL.
//
// The OTLP HTTP exporters' WithEndpointURL option parses the URL and stores its
// path verbatim — including an empty one. An empty path is then treated as an
// explicit choice, so the exporter's own default ("/v1/logs" etc.) is never
// applied and every request is POSTed to the collector root, which answers 404.
// Building the full URL here keeps the base endpoint configurable while making
// sure the signal path is always present.
func signalEndpoint(base, signalPath string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return signalPath
	}
	return base + signalPath
}

// ---------------------------------------------------------------------------
// small env helpers
// ---------------------------------------------------------------------------

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	// OTEL_METRIC_EXPORT_INTERVAL is specified in seconds.
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return fallback
}
