package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Handle processes a single delivery. In a real system it would send an
// email / push notification; here we log and simulate the work.
func (c *Consumer) handle(ctx context.Context, d amqp.Delivery) error {
	var envelope struct {
		EventID   string `json:"event_id"`
		EventType string `json:"event_type"`
		OrderID   uint   `json:"order_id"`
		UserID    uint   `json:"user_id"`
	}
	if err := json.Unmarshal(d.Body, &envelope); err != nil {
		return fmt.Errorf("unmarshal event: %w", err)
	}

	switch envelope.EventType {
	case "order.created":
		log.Printf("[notify] order %d created — sending confirmation to user %d (event %s)",
			envelope.OrderID, envelope.UserID, envelope.EventID)
	case "order.cancelled":
		log.Printf("[notify] order %d cancelled — sending cancellation notice to user %d (event %s)",
			envelope.OrderID, envelope.UserID, envelope.EventID)
	case "order.completed":
		log.Printf("[notify] order %d completed — sending delivery update to user %d (event %s)",
			envelope.OrderID, envelope.UserID, envelope.EventID)
	default:
		log.Printf("[notify] unknown event type %q (event %s) — ignoring", envelope.EventType, envelope.EventID)
	}

	// Simulate slow notification work.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(100 * time.Millisecond):
	}
	return nil
}
