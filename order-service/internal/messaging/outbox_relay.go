package messaging

import (
	"context"
	"log"
	"time"

	"order-service/internal/repository"
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
type OutboxRelay struct {
	outboxRepo *repository.OutboxRepository
	publisher  *Publisher
	batchSize  int
	interval   time.Duration
	stopCh     chan struct{}
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
	}
}

// Start launches the relay loop in a background goroutine.
func (r *OutboxRelay) Start() {
	go r.run()
	log.Printf("[outbox-relay] started (batch=%d, interval=%s)", r.batchSize, r.interval)
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
			log.Println("[outbox-relay] stopping")
			return
		case <-ticker.C:
			r.pollAndPublish()
		}
	}
}

// pollAndPublish fetches one batch of pending outbox events and publishes each
// one to RabbitMQ. Each event is published in its own short-lived transaction:
//   1. SELECT ... FOR UPDATE SKIP LOCKED (inside GetPending)
//   2. Publish to RabbitMQ with publisher confirms
//   3. Mark as published in the DB
//
// If the relay crashes between steps 2 and 3, the event stays pending and
// will be republished on the next cycle (at-least-once delivery).
func (r *OutboxRelay) pollAndPublish() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	events, err := r.outboxRepo.GetPending(ctx, r.batchSize)
	if err != nil {
		log.Printf("[outbox-relay] failed to fetch pending events: %v", err)
		return
	}
	if len(events) == 0 {
		return
	}

	published := 0
	for _, event := range events {
		if err := r.publisher.PublishRaw(
			ctx,
			event.RoutingKey,
			event.EventID,
			event.EventType,
			event.Payload,
		); err != nil {
			log.Printf("[outbox-relay] failed to publish event %s (type %s, order %d): %v",
				event.EventID, event.EventType, event.AggregateID, err)
			// Stop processing this batch — the next tick will retry from the
			// same point since the row is still pending.
			break
		}

		if err := r.outboxRepo.MarkPublished(ctx, event.ID); err != nil {
			log.Printf("[outbox-relay] failed to mark event %d published: %v", event.ID, err)
			// The event WAS published to RabbitMQ but we couldn't update the
			// DB row. On the next cycle it will be republished (duplicate).
			// Consumers are idempotent, so this is safe.
			break
		}
		published++
	}

	if published > 0 {
		log.Printf("[outbox-relay] published %d/%d outbox events", published, len(events))
	}
}
