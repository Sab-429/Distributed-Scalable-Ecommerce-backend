package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type workerInstruments struct {
	tasks    metric.Int64Counter
	failures metric.Int64Counter
	duration metric.Float64Histogram
}

var workerInstr = newInstruments("observability/worker", func(m metric.Meter) workerInstruments {
	tasks, _ := m.Int64Counter("app.worker.tasks",
		metric.WithDescription("Background worker tasks executed"),
		metric.WithUnit("{task}"),
	)
	failures, _ := m.Int64Counter("app.worker.failures",
		metric.WithDescription("Background worker task failures"),
		metric.WithUnit("{failure}"),
	)
	duration, _ := m.Float64Histogram("app.worker.task.duration",
		metric.WithDescription("Background worker task duration"),
		metric.WithUnit("s"),
	)
	return workerInstruments{tasks: tasks, failures: failures, duration: duration}
})

// Worker instruments background goroutines: the outbox relays, the refresh
// token cleaner and the notification worker.
//
//	w := telemetry.NewWorker("order-service.outbox")
//	ctx, span, done := w.Start(ctx, "poll")
//	err := doWork(ctx)
//	done(err)
type Worker struct {
	name   string
	tracer trace.Tracer
}

// NewWorker registers a named worker. Call it after telemetry.Init.
func NewWorker(name string) *Worker {
	return &Worker{name: name, tracer: otel.Tracer("observability/worker")}
}

// Start opens a span for one task iteration and returns a done callback.
// The callback must be called exactly once (typically via defer).
func (w *Worker) Start(ctx context.Context, task string, attrs ...attribute.KeyValue) (context.Context, trace.Span, func(error)) {
	base := append([]attribute.KeyValue{
		attribute.String("worker.name", w.name),
		attribute.String("worker.task", task),
	}, attrs...)

	ctx, span := w.tracer.Start(ctx, w.name+" "+task,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(base...),
	)
	start := time.Now()

	var ended bool
	done := func(err error) {
		if ended {
			return
		}
		ended = true

		elapsed := time.Since(start).Seconds()
		outcome := OutcomeSuccess
		if err != nil {
			outcome = OutcomeError
			span.SetStatus(codes.Error, err.Error())
			span.RecordError(err)
		}
		resultAttrs := append(base, attribute.String("outcome", outcome))

		instr := workerInstr.Get()
		instr.tasks.Add(ctx, 1, metric.WithAttributes(resultAttrs...))
		instr.duration.Record(ctx, elapsed, metric.WithAttributes(resultAttrs...))
		if err != nil {
			instr.failures.Add(ctx, 1, metric.WithAttributes(resultAttrs...))
		}
		span.End()
	}
	return ctx, span, done
}

// RecordFailure counts a worker failure outside of a Start/done pair (for
// example a consumer giving up on a message).
func RecordFailure(ctx context.Context, worker, reason string, err error) {
	attrs := []attribute.KeyValue{
		attribute.String("worker.name", worker),
		attribute.String("reason", reason),
	}
	if err != nil {
		attrs = append(attrs, attribute.String("error.type", err.Error()))
	}
	workerInstr.Get().failures.Add(ctx, 1, metric.WithAttributes(attrs...))
}
