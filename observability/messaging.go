package telemetry

import (
	"context"
	"fmt"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// MessageOutcome values reported by the messaging metrics.
const (
	OutcomeSuccess    = "success"
	OutcomeError      = "error"
	OutcomeRetry      = "retry"
	OutcomeDeadLetter = "dead_letter"
)

// MessageInfo describes the AMQP message being published or consumed. It is
// deliberately tiny so callers can build it inline.
type MessageInfo struct {
	Exchange   string // publishing only
	RoutingKey string // publishing only
	Queue      string // consuming only
	MessageID  string
	EventType  string // e.g. "order.created"
}

// ---------------------------------------------------------------------------
// metrics
// ---------------------------------------------------------------------------

type messagingInstruments struct {
	published    metric.Int64Counter
	publishDur   metric.Float64Histogram
	processed    metric.Int64Counter
	processDur   metric.Float64Histogram
	failures     metric.Int64Counter
	retries      metric.Int64Counter
	deadLettered metric.Int64Counter
}

var messagingInstr = newInstruments("observability/messaging", func(m metric.Meter) messagingInstruments {
	published, _ := m.Int64Counter("messaging.publish.messages",
		metric.WithDescription("Messages published to RabbitMQ"),
		metric.WithUnit("{message}"),
	)
	publishDur, _ := m.Float64Histogram("messaging.publish.duration",
		metric.WithDescription("Publish duration including broker confirm"),
		metric.WithUnit("s"),
	)
	processed, _ := m.Int64Counter("messaging.process.messages",
		metric.WithDescription("Messages processed by a consumer"),
		metric.WithUnit("{message}"),
	)
	processDur, _ := m.Float64Histogram("messaging.process.duration",
		metric.WithDescription("Message processing duration"),
		metric.WithUnit("s"),
	)
	failures, _ := m.Int64Counter("messaging.process.failures",
		metric.WithDescription("Consumer / worker failures (retries exhausted or handler errors)"),
		metric.WithUnit("{failure}"),
	)
	retries, _ := m.Int64Counter("messaging.process.retries",
		metric.WithDescription("Messages republished to the retry exchange"),
		metric.WithUnit("{message}"),
	)
	deadLettered, _ := m.Int64Counter("messaging.process.dead_lettered",
		metric.WithDescription("Messages moved to a dead-letter queue"),
		metric.WithUnit("{message}"),
	)
	return messagingInstruments{
		published: published, publishDur: publishDur,
		processed: processed, processDur: processDur,
		failures: failures, retries: retries, deadLettered: deadLettered,
	}
})

// ---------------------------------------------------------------------------
// W3C propagation over AMQP headers
// ---------------------------------------------------------------------------

// amqpCarrier adapts an AMQP header table to propagation.TextMapCarrier.
// Header keys are case-insensitive for lookups, values may arrive as string or
// []byte depending on the publishing client.
type amqpCarrier amqp.Table

func (c amqpCarrier) Get(key string) string {
	for k, v := range c {
		if strings.EqualFold(k, key) {
			switch t := v.(type) {
			case string:
				return t
			case []byte:
				return string(t)
			case fmt.Stringer:
				return t.String()
			default:
				if v == nil {
					return ""
				}
				return fmt.Sprint(v)
			}
		}
	}
	return ""
}

func (c amqpCarrier) Set(key, value string) { c[key] = value }

func (c amqpCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// InjectTraceContext writes the current trace context (traceparent /
// tracestate / baggage) into the AMQP headers of an outgoing message.
// Returns the table so `headers == nil` callers can use the result directly.
func InjectTraceContext(ctx context.Context, headers amqp.Table) amqp.Table {
	if headers == nil {
		headers = amqp.Table{}
	}
	otel.GetTextMapPropagator().Inject(ctx, amqpCarrier(headers))
	return headers
}

// ExtractTraceContext reads the trace context out of AMQP headers so the
// consumer continues the producer's trace.
func ExtractTraceContext(ctx context.Context, headers amqp.Table) context.Context {
	if len(headers) == 0 {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, amqpCarrier(headers))
}

// ---------------------------------------------------------------------------
// publish / consume spans
// ---------------------------------------------------------------------------

// StartPublish opens a producer span for an outgoing message and injects the
// trace context into the returned header table:
//
//	ctx, span, headers := telemetry.StartPublish(ctx, info, nil)
//	defer telemetry.FinishPublish(ctx, span, start, info, err)
//	channel.PublishWithContext(ctx, exchange, rk, false, false, amqp.Publishing{Headers: headers, ...})
func StartPublish(ctx context.Context, info MessageInfo, headers amqp.Table) (context.Context, trace.Span, amqp.Table) {
	headers = InjectTraceContext(ctx, headers)

	name := "rabbitmq publish"
	if info.RoutingKey != "" {
		name += " " + info.RoutingKey
	} else if info.Exchange != "" {
		name += " " + info.Exchange
	}

	ctx, span := otel.Tracer("observability/rabbitmq").Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(messagingAttrs(info, "send")...),
	)
	return ctx, span, headers
}

// FinishPublish ends the producer span and records publish metrics.
func FinishPublish(ctx context.Context, span trace.Span, start time.Time, info MessageInfo, err error) {
	elapsed := time.Since(start).Seconds()
	attrs := append(messagingAttrs(info, "send"), attribute.String("outcome", outcomeOf(err)))

	instr := messagingInstr.Get()
	instr.published.Add(ctx, 1, metric.WithAttributes(attrs...))
	instr.publishDur.Record(ctx, elapsed, metric.WithAttributes(attrs...))

	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	}
	span.End()
}

// StartConsume opens a consumer span that is linked to the producer's trace by
// extracting the W3C context from the delivery headers:
//
//	ctx, span := telemetry.StartConsume(ctx, info, d.Headers)
//	defer telemetry.FinishConsume(ctx, span, start, info, outcome, err)
func StartConsume(ctx context.Context, info MessageInfo, headers amqp.Table) (context.Context, trace.Span) {
	ctx = ExtractTraceContext(ctx, headers)

	name := "rabbitmq process"
	if info.Queue != "" {
		name += " " + info.Queue
	} else if info.RoutingKey != "" {
		name += " " + info.RoutingKey
	}

	ctx, span := otel.Tracer("observability/rabbitmq").Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(messagingAttrs(info, "process")...),
	)
	return ctx, span
}

// FinishConsume ends the consumer span and records process metrics. outcome is
// one of OutcomeSuccess / OutcomeRetry / OutcomeDeadLetter / OutcomeError.
func FinishConsume(ctx context.Context, span trace.Span, start time.Time, info MessageInfo, outcome string, err error) {
	elapsed := time.Since(start).Seconds()
	attrs := append(messagingAttrs(info, "process"), attribute.String("outcome", outcome))

	instr := messagingInstr.Get()
	instr.processed.Add(ctx, 1, metric.WithAttributes(attrs...))
	instr.processDur.Record(ctx, elapsed, metric.WithAttributes(attrs...))

	switch outcome {
	case OutcomeRetry:
		instr.retries.Add(ctx, 1, metric.WithAttributes(attrs...))
	case OutcomeDeadLetter:
		instr.deadLettered.Add(ctx, 1, metric.WithAttributes(attrs...))
		instr.failures.Add(ctx, 1, metric.WithAttributes(attrs...))
	case OutcomeError:
		instr.failures.Add(ctx, 1, metric.WithAttributes(attrs...))
	}

	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	}
	span.End()
}

func messagingAttrs(info MessageInfo, operation string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("messaging.system", "rabbitmq"),
		attribute.String("messaging.operation", operation),
	}
	switch operation {
	case "send":
		if info.Exchange != "" {
			attrs = append(attrs,
				attribute.String("messaging.destination.name", info.Exchange),
				attribute.String("messaging.destination_kind", "exchange"),
			)
		}
		if info.RoutingKey != "" {
			attrs = append(attrs, attribute.String("messaging.rabbitmq.routing_key", info.RoutingKey))
		}
	default:
		if info.Queue != "" {
			attrs = append(attrs,
				attribute.String("messaging.destination.name", info.Queue),
				attribute.String("messaging.destination_kind", "queue"),
			)
		}
		if info.RoutingKey != "" {
			attrs = append(attrs, attribute.String("messaging.rabbitmq.routing_key", info.RoutingKey))
		}
	}
	if info.MessageID != "" {
		attrs = append(attrs, attribute.String("messaging.message.id", info.MessageID))
	}
	if info.EventType != "" {
		attrs = append(attrs, attribute.String("messaging.message.type", info.EventType))
	}
	return attrs
}
