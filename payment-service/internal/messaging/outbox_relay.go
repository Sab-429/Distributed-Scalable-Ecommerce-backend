package messaging

import (
	"context"
	"time"

	"payment-service/internal/repository"

	"observability"
)

// OutboxRelay is a background worker that polls the outbox table for pending
// events, publishes them to RabbitMQ via publisher confirms, and marks them
// as published. At-least-once delivery: if it crashes after RabbitMQ confirms
// but before marking the row, the row stays pending and gets republished next
// cycle. Consumers are idempotent (ConfirmOrder guards on status=pending).
//
// Observability: every poll runs inside a "payment-service.outbox poll" span
// and the trace context stored on the outbox row is restored before publishing,
// so the broker hop and all consumers stay in the originating request's trace.
type OutboxRelay struct {
	outboxRepo *repository.OutboxRepository
	publisher  *Publisher
	batchSize  int
	interval   time.Duration
	stopCh     chan struct{}
	worker     *telemetry.Worker
}

func NewOutboxRelay(
	outboxRepo *repository.OutboxRepository,
	publisher *Publisher,
	batchSize int,
	interval time.Duration,
) *OutboxRelay {
	return &OutboxRelay{
		outboxRepo: outboxRepo,
		publisher:  publisher,
		batchSize:  batchSize,
		interval:   interval,
		stopCh:     make(chan struct{}),
		worker:     telemetry.NewWorker("payment-service.outbox"),
	}
}

func (r *OutboxRelay) Start() {
	go r.run()
	telemetry.L(context.Background()).InfoContext(context.Background(),
		"outbox relay started", "batch", r.batchSize, "interval", r.interval.String())
}

func (r *OutboxRelay) Stop() {
	close(r.stopCh)
}

func (r *OutboxRelay) run() {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			telemetry.L(context.Background()).InfoContext(context.Background(), "outbox relay stopping")
			return
		case <-ticker.C:
			r.pollAndPublish()
		}
	}
}

// pollAndPublish wraps one poll cycle in a worker span + metrics.
func (r *OutboxRelay) pollAndPublish() {
	ctx, _, done := r.worker.Start(context.Background(), "poll")
	done(r.pollAndPublishCtx(ctx))
}

func (r *OutboxRelay) pollAndPublishCtx(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	events, err := r.outboxRepo.GetPending(ctx, r.batchSize)
	if err != nil {
		telemetry.L(ctx).ErrorContext(ctx, "failed to fetch pending outbox events", "error", err)
		return err
	}
	if len(events) == 0 {
		return nil
	}

	published := 0
	var firstErr error

	for _, event := range events {
		// Re-attach the trace of the request that wrote this outbox row so
		// the async chain shares one trace.
		publishCtx := telemetry.ContextWithTraceparent(ctx, event.TraceContext)

		if err := r.publisher.PublishRaw(
			publishCtx,
			event.RoutingKey,
			event.EventID,
			event.EventType,
			event.Payload,
		); err != nil {
			telemetry.L(ctx).ErrorContext(ctx, "failed to publish outbox event",
				"event_id", event.EventID,
				"event_type", event.EventType,
				"aggregate_id", event.AggregateID,
				"error", err,
			)
			firstErr = err
			break
		}

		if err := r.outboxRepo.MarkPublished(ctx, event.ID); err != nil {
			telemetry.L(ctx).ErrorContext(ctx, "failed to mark outbox event published",
				"outbox_id", event.ID, "event_id", event.EventID, "error", err)
			firstErr = err
			break
		}
		published++
	}

	if published > 0 {
		telemetry.L(ctx).InfoContext(ctx, "published outbox events",
			"published", published, "batch", len(events))
	}
	return firstErr
}
