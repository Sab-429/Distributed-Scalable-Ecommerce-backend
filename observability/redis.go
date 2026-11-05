package telemetry

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// redisInstruments are the Redis client metrics (shared semconv-style names
// with PostgreSQL so one dashboard can cover both data stores).
type redisInstruments struct {
	duration   metric.Float64Histogram
	operations metric.Int64Counter
}

var redisInstr = newInstruments("observability/redis", func(m metric.Meter) redisInstruments {
	duration, _ := m.Float64Histogram("db.client.operation.duration",
		metric.WithDescription("Duration of Redis operations"),
		metric.WithUnit("s"),
	)
	operations, _ := m.Int64Counter("db.client.operations",
		metric.WithDescription("Number of Redis operations by outcome"),
		metric.WithUnit("{operation}"),
	)
	return redisInstruments{duration: duration, operations: operations}
})

// RedisHook returns a go-redis hook that creates a span per command and
// records db.client.operation.duration / db.client.operations with
// db.system.name=redis.
//
//	redisClient := redis.NewClient(...)
//	redisClient.AddHook(telemetry.RedisHook())
//
// Build the client after telemetry.Init so the tracer is the real one.
func RedisHook() redis.Hook {
	return &redisHook{tracer: otel.Tracer("observability/redis")}
}

type redisHook struct {
	tracer trace.Tracer
}

func (h *redisHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		ctx, span := h.tracer.Start(ctx, "redis.connect",
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				attribute.String("db.system.name", "redis"),
				attribute.String("network.transport", network),
				attribute.String("server.address", addr),
			),
		)
		conn, err := next(ctx, network, addr)
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
		return conn, err
	}
}

func (h *redisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		name := strings.ToUpper(cmd.Name())
		ctx, span := h.tracer.Start(ctx, "redis."+name,
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				attribute.String("db.system.name", "redis"),
				attribute.String("db.operation.name", name),
			),
		)
		start := time.Now()
		err := next(ctx, cmd)

		attrs := []attribute.KeyValue{
			attribute.String("db.system.name", "redis"),
			attribute.String("db.operation.name", name),
			attribute.String("outcome", outcomeOf(err)),
		}
		instr := redisInstr.Get()
		instr.duration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attrs...))
		instr.operations.Add(ctx, 1, metric.WithAttributes(attrs...))

		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
		return err
	}
}

func (h *redisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		ctx, span := h.tracer.Start(ctx, "redis.pipeline",
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				attribute.String("db.system.name", "redis"),
				attribute.String("db.operation.name", "PIPELINE"),
				attribute.Int("db.operation.batch.size", len(cmds)),
			),
		)
		start := time.Now()
		err := next(ctx, cmds)

		attrs := []attribute.KeyValue{
			attribute.String("db.system.name", "redis"),
			attribute.String("db.operation.name", "PIPELINE"),
			attribute.String("outcome", outcomeOf(err)),
		}
		instr := redisInstr.Get()
		instr.duration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(attrs...))
		instr.operations.Add(ctx, 1, metric.WithAttributes(attrs...))

		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			span.RecordError(err)
		}
		span.End()
		return err
	}
}

func outcomeOf(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

// ---------------------------------------------------------------------------
// cache hit / miss metrics
// ---------------------------------------------------------------------------

type cacheInstruments struct {
	lookups metric.Int64Counter
}

var cacheInstr = newInstruments("observability/cache", func(m metric.Meter) cacheInstruments {
	lookups, _ := m.Int64Counter("app.cache.lookups",
		metric.WithDescription("Redis cache lookups by result (hit/miss)"),
		metric.WithUnit("{lookup}"),
	)
	return cacheInstruments{lookups: lookups}
})

// CacheLookup records a cache lookup so hit ratio can be charted per cache
// (e.g. cache.name=product | gateway.response).
func CacheLookup(ctx context.Context, cacheName string, hit bool) {
	result := "miss"
	if hit {
		result = "hit"
	}
	cacheInstr.Get().lookups.Add(ctx, 1, metric.WithAttributes(
		attribute.String("cache.name", cacheName),
		attribute.String("result", result),
	))
}
