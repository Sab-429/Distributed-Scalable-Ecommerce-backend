package messaging

import (
	"context"
	"time"

	"order-service/internal/repository"

	"observability"
)

// OutboxRelay is a background worker that polls the outbox table for pending
// events, publishes them to RabbitMQ via publisher confirms, and marks them
// as published. It runs in a fixed-interval loop and shuts down cleanly on
// context cancellation.
//
// The relay guarantees at-least-once delivery: if it crashes after RabbitMQ
// confirms a message but before marking the row published, the row stays
// pending and will be republished on the next poll cycle. Consumers must be
// idempotent (they already are — see ConfirmOrder's status guard).
//
// Observability: every poll runs inside an "order-service.outbox poll" span
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

// NewOutboxRelay creates a new relay. The relay starts when Start() is called.
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
		worker:     telemetry.NewWorker("order-service.outbox"),
	}
}

// Start launches the relay loop in a background goroutine.
func (r *OutboxRelay) Start() {
	go r.run()
	telemetry.L(context.Background()).InfoContext(context.Background(),
		"outbox relay started", "batch", r.batchSize, "interval", r.interval.String())
}

// Stop signals the relay to stop after the current poll cycle completes.
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

// pollAndPublishCtx fetches one batch of pending outbox events and publishes
// each one to RabbitMQ. Each event is published in its own short-lived
// transaction:
//  1. SELECT ... FOR UPDATE SKIP LOCKED (inside GetPending)
//  2. Publish to RabbitMQ with publisher confirms
//  3. Mark as published in the DB
//
// If the relay crashes between steps 2 and 3, the event stays pending and
// will be republished on the next cycle (at-least-once delivery).
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
		// Re-attach the trace of the HTTP request (or RPC) that wrote this
		// outbox row so the whole async chain shares one trace.
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
			// Stop processing this batch — the next tick will retry from the
			// same point since the row is still pending.
			firstErr = err
			break
		}

		if err := r.outboxRepo.MarkPublished(ctx, event.ID); err != nil {
			telemetry.L(ctx).ErrorContext(ctx, "failed to mark outbox event published",
				"outbox_id", event.ID, "event_id", event.EventID, "error", err)
			// The event WAS published to RabbitMQ but we couldn't update the
			// DB row. On the next cycle it will be republished (duplicate).
			// Consumers are idempotent, so this is safe.
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
