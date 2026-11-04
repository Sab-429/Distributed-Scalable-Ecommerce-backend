package messaging

import (
	"context"
	"log"
	"time"

	"payment-service/internal/repository"
)

// OutboxRelay is a background worker that polls the outbox table for pending
// events, publishes them to RabbitMQ via publisher confirms, and marks them
// as published. At-least-once delivery: if it crashes after RabbitMQ confirms
// but before marking the row, the row stays pending and gets republished next
// cycle. Consumers are idempotent (ConfirmOrder guards on status=pending).
type OutboxRelay struct {
	outboxRepo *repository.OutboxRepository
	publisher  *Publisher
	batchSize  int
	interval   time.Duration
	stopCh     chan struct{}
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
	}
}

func (r *OutboxRelay) Start() {
	go r.run()
	log.Printf("[outbox-relay] started (batch=%d, interval=%s)", r.batchSize, r.interval)
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
			log.Println("[outbox-relay] stopping")
			return
		case <-ticker.C:
			r.pollAndPublish()
		}
	}
}

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
			log.Printf("[outbox-relay] failed to publish event %s (type %s, payment %d): %v",
				event.EventID, event.EventType, event.AggregateID, err)
			break
		}

		if err := r.outboxRepo.MarkPublished(ctx, event.ID); err != nil {
			log.Printf("[outbox-relay] failed to mark event %d published: %v", event.ID, err)
			break
		}
		published++
	}

	if published > 0 {
		log.Printf("[outbox-relay] published %d/%d outbox events", published, len(events))
	}
}
