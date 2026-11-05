package telemetry

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// GinMiddleware traces every Gin endpoint: it extracts the incoming W3C
// context, creates a server span named after the matched route and records
// the standard http.server.* metrics (request rate, latency histogram, active
// requests, error counts) attributed by method / route / status code.
//
// Register it as the FIRST middleware so every other middleware and handler
// runs inside the request span.
func GinMiddleware(service string) gin.HandlerFunc {
	return otelgin.Middleware(service)
}

// proxyInstruments are the client-side metrics of the API gateway proxies.
type proxyInstruments struct {
	requests   metric.Int64Counter
	duration   metric.Float64Histogram
	active     metric.Int64UpDownCounter
	errCounter metric.Int64Counter
}

var proxyInstr = newInstruments("observability/proxy", func(m metric.Meter) proxyInstruments {
	requests, _ := m.Int64Counter("http.client.requests",
		metric.WithDescription("Outbound HTTP requests issued by the reverse proxies"),
		metric.WithUnit("{request}"),
	)
	duration, _ := m.Float64Histogram("http.client.request.duration",
		metric.WithDescription("Outbound HTTP request duration"),
		metric.WithUnit("s"),
	)
	active, _ := m.Int64UpDownCounter("http.client.active_requests",
		metric.WithDescription("Outbound HTTP requests currently in flight"),
		metric.WithUnit("{request}"),
	)
	failures, _ := m.Int64Counter("http.client.errors",
		metric.WithDescription("Outbound HTTP requests that returned 5xx or failed"),
		metric.WithUnit("{error}"),
	)
	return proxyInstruments{requests: requests, duration: duration, active: active, errCounter: failures}
})

// ProxyTracing wraps a reverse proxy handler with a client span and injects
// the W3C trace context into the proxied request headers, so the upstream
// service continues the exact same distributed trace:
//
//	client → [gateway server span] → [proxy client span] → [order service span]
func ProxyTracing(next http.Handler) http.Handler {
	tracer := otel.Tracer("observability/proxy")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(
			r.Context(),
			propagation.HeaderCarrier(r.Header),
		)

		spanName := "HTTP " + r.Method + " " + r.URL.Path
		ctx, span := tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
				attribute.String("server.address", r.Host),
				attribute.String("network.protocol.name", "http"),
				attribute.String("component", "reverse-proxy"),
			),
		)
		defer span.End()

		// Hand the upstream the current trace context.
		otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(r.Header))

		instr := proxyInstr.Get()
		instr.active.Add(ctx, 1, metric.WithAttributes(
			attribute.String("http.request.method", r.Method),
		))
		start := time.Now()

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(ctx))

		elapsed := time.Since(start).Seconds()
		attrs := []attribute.KeyValue{
			attribute.String("http.request.method", r.Method),
			attribute.Int("http.response.status_code", rec.status),
			attribute.String("url.path", r.URL.Path),
		}
		instr.active.Add(ctx, -1, metric.WithAttributes(
			attribute.String("http.request.method", r.Method),
		))
		instr.duration.Record(ctx, elapsed, metric.WithAttributes(attrs...))
		instr.requests.Add(ctx, 1, metric.WithAttributes(attrs...))
		if rec.status >= http.StatusInternalServerError {
			instr.errCounter.Add(ctx, 1, metric.WithAttributes(attrs...))
		}

		span.SetAttributes(attribute.Int("http.response.status_code", rec.status))
		if rec.status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, "upstream returned "+strconv.Itoa(rec.status))
		}
	})
}

// statusRecorder captures the status code written by the proxied handler while
// staying transparent for Flush/Unwrap (used by httputil.ReverseProxy).
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(b)
}

// Flush implements http.Flusher so streaming responses keep working.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the original writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// RequestLogger emits one structured JSON log line per HTTP request. The
// shared logger handler injects trace_id / span_id from the request context,
// so every line is joinable with its Jaeger span.
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		ctx := c.Request.Context()
		requestID := c.GetString("request_id")

		L(ctx).InfoContext(ctx, "http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"route", c.FullPath(),
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", requestID,
			"client_ip", c.ClientIP(),
		)
	}
}

// NormalizeRoute returns the matched Gin route (e.g. "/api/v1/orders/:id") and
// falls back to the raw path when no route matched.
func NormalizeRoute(c *gin.Context) string {
	if route := c.FullPath(); route != "" {
		return route
	}
	return strings.TrimSpace(c.Request.URL.Path)
}
