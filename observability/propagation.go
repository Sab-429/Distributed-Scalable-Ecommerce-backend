package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
)

// stringCarrier is a trivial TextMapCarrier over map[string]string, used to
// move the W3C trace context through non-HTTP carriers (outbox columns,
// database columns, job payloads).
type stringCarrier map[string]string

func (c stringCarrier) Get(key string) string { return c[key] }
func (c stringCarrier) Set(key, value string) { c[key] = value }
func (c stringCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// TraceparentFromContext returns the W3C "traceparent" header value of ctx, or
// "" when ctx carries no valid span context. Store it next to a queued job
// (outbox row, cron payload, ...) so the work it triggers still belongs to the
// original trace.
func TraceparentFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	carrier := stringCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier["traceparent"]
}

// ContextWithTraceparent restores the trace context written by
// TraceparentFromContext. Invalid or empty values return ctx unchanged.
func ContextWithTraceparent(ctx context.Context, traceparent string) context.Context {
	if traceparent == "" {
		return ctx
	}
	return propagation.TraceContext{}.Extract(ctx, stringCarrier{"traceparent": traceparent})
}
