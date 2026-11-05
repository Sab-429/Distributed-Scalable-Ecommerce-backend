package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

// LoggerName is the instrumentation scope used for OTLP log records.
const LoggerName = "observability/logger"

// InitLogger installs the process-wide structured logger:
//
//   - a JSON handler on stdout (start.sh redirects it to logs/<service>.log);
//   - an OTLP handler that ships every record to the OpenTelemetry Collector,
//     which forwards them to Loki.
//
// Both handlers attach trace_id and span_id taken from the context, so log
// lines can be correlated with Jaeger traces. Call InitLogger before Init to
// capture startup logs as well; the OTLP handler resolves the logger provider
// lazily, so ordering does not matter.
func InitLogger(service string) *slog.Logger {
	level := parseLevel(os.Getenv("LOG_LEVEL"))

	handlers := []slog.Handler{
		&traceIDHandler{
			next:    slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}),
			service: service,
		},
		&otelLogHandler{service: service, level: level},
	}

	l := slog.New(&multiHandler{handlers: handlers})
	slog.SetDefault(l)
	return l
}

// L returns the process-wide structured logger. Pass the same ctx to the
// *Context methods (InfoContext, ErrorContext, ...) so trace_id/span_id are
// attached to the record.
func L(context.Context) *slog.Logger {
	return slog.Default()
}

// ---------------------------------------------------------------------------
// handlers
// ---------------------------------------------------------------------------

// traceIDHandler decorates a JSON log line with service.name, trace_id and
// span_id.
type traceIDHandler struct {
	next    slog.Handler
	service string
}

func (h *traceIDHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *traceIDHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.service != "" {
		r.AddAttrs(slog.String("service.name", h.service))
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.next.Handle(ctx, r)
}

func (h *traceIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceIDHandler{next: h.next.WithAttrs(attrs), service: h.service}
}

func (h *traceIDHandler) WithGroup(name string) slog.Handler {
	return &traceIDHandler{next: h.next.WithGroup(name), service: h.service}
}

// otelLogHandler forwards records to the OTLP logger provider so they land in
// Loki with severity, attributes and trace correlation.
type otelLogHandler struct {
	service string
	level   slog.Level
	// preset holds attributes added through WithAttrs, group the key prefix
	// applied by WithGroup.
	preset []slog.Attr
	group  []string
}

func (h *otelLogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *otelLogHandler) Handle(ctx context.Context, r slog.Record) error {
	// Resolved on every record so the provider installed by Init is picked up
	// regardless of whether the logger was created before or after Init.
	provider := otel.GetLoggerProvider()
	if provider == nil {
		return nil
	}

	logger := provider.Logger(LoggerName, otellog.WithInstrumentationVersion("1.0.0"))

	var rec otellog.Record
	ts := r.Time
	if ts.IsZero() {
		ts = time.Now()
	}
	rec.SetTimestamp(ts)
	rec.SetObservedTimestamp(time.Now())
	sev, text := otelSeverity(r.Level)
	rec.SetSeverity(sev)
	rec.SetSeverityText(text)
	rec.SetBody(attribute.StringValue(r.Message))

	if h.service != "" {
		rec.AddAttributes(attribute.String("service.name", h.service))
	}

	for _, a := range h.preset {
		rec.AddAttributes(slogAttrToOTel(a)...)
	}

	prefix := strings.Join(h.group, ".")
	r.Attrs(func(a slog.Attr) bool {
		if prefix != "" {
			a.Key = prefix + "." + a.Key
		}
		rec.AddAttributes(slogAttrToOTel(a)...)
		return true
	})

	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		rec.AddAttributes(
			attribute.String("trace_id", sc.TraceID().String()),
			attribute.String("span_id", sc.SpanID().String()),
		)
	}

	logger.Emit(ctx, rec)
	return nil
}

func (h *otelLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	child := &otelLogHandler{
		service: h.service,
		level:   h.level,
		preset:  append(append([]slog.Attr{}, h.preset...), attrs...),
		group:   append([]string{}, h.group...),
	}
	return child
}

func (h *otelLogHandler) WithGroup(name string) slog.Handler {
	child := &otelLogHandler{
		service: h.service,
		level:   h.level,
		preset:  append([]slog.Attr{}, h.preset...),
		group:   append(append([]string{}, h.group...), name),
	}
	return child
}

// multiHandler fans a record out to every configured handler.
type multiHandler struct {
	handlers []slog.Handler
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range m.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{handlers: next}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithGroup(name)
	}
	return &multiHandler{handlers: next}
}

// ---------------------------------------------------------------------------
// conversions
// ---------------------------------------------------------------------------

func slogAttrToOTel(a slog.Attr) []attribute.KeyValue {
	if a.Equal(slog.Attr{}) {
		return nil
	}
	key := a.Key
	switch a.Value.Kind() {
	case slog.KindGroup:
		out := make([]attribute.KeyValue, 0, len(a.Value.Group()))
		for _, ga := range a.Value.Group() {
			ga.Key = key + "." + ga.Key
			out = append(out, slogAttrToOTel(ga)...)
		}
		return out
	case slog.KindString:
		return []attribute.KeyValue{attribute.String(key, a.Value.String())}
	case slog.KindInt64:
		return []attribute.KeyValue{attribute.Int64(key, a.Value.Int64())}
	case slog.KindUint64:
		return []attribute.KeyValue{attribute.Int64(key, int64(a.Value.Uint64()))}
	case slog.KindFloat64:
		return []attribute.KeyValue{attribute.Float64(key, a.Value.Float64())}
	case slog.KindBool:
		return []attribute.KeyValue{attribute.Bool(key, a.Value.Bool())}
	case slog.KindTime:
		return []attribute.KeyValue{attribute.String(key, a.Value.Time().Format(time.RFC3339Nano))}
	case slog.KindDuration:
		return []attribute.KeyValue{attribute.String(key, a.Value.Duration().String())}
	case slog.KindAny:
		return []attribute.KeyValue{attribute.String(key, anyToString(a.Value.Any()))}
	default:
		return []attribute.KeyValue{attribute.String(key, a.Value.String())}
	}
}

func anyToString(v any) string {
	switch t := v.(type) {
	case error:
		return t.Error()
	case string:
		return t
	case []byte:
		return string(t)
	case nil:
		return "<nil>"
	default:
		return strings.TrimSpace(slog.Any("", v).String())
	}
}

func otelSeverity(level slog.Level) (otellog.Severity, string) {
	switch {
	case level >= slog.LevelError:
		return otellog.SeverityError, level.String()
	case level >= slog.LevelWarn:
		return otellog.SeverityWarn, level.String()
	case level >= slog.LevelInfo:
		return otellog.SeverityInfo, level.String()
	default:
		return otellog.SeverityDebug, level.String()
	}
}

func parseLevel(v string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
