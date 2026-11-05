package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"observability"
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
		telemetry.L(ctx).InfoContext(ctx, "sending order confirmation",
			"order_id", envelope.OrderID,
			"user_id", envelope.UserID,
			"event_id", envelope.EventID,
			"event_type", envelope.EventType,
		)
	case "order.cancelled":
		telemetry.L(ctx).InfoContext(ctx, "sending cancellation notice",
			"order_id", envelope.OrderID,
			"user_id", envelope.UserID,
			"event_id", envelope.EventID,
			"event_type", envelope.EventType,
		)
	case "order.completed":
		telemetry.L(ctx).InfoContext(ctx, "sending delivery update",
			"order_id", envelope.OrderID,
			"user_id", envelope.UserID,
			"event_id", envelope.EventID,
			"event_type", envelope.EventType,
		)
	default:
		telemetry.L(ctx).WarnContext(ctx, "unknown event type, ignoring",
			"event_type", envelope.EventType,
			"event_id", envelope.EventID,
		)
	}

	// Simulate slow notification work.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(100 * time.Millisecond):
	}
	return nil
}
