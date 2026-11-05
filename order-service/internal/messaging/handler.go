package messaging

import (
	"context"
	"encoding/json"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"

	"observability"
)

// handle processes a single payment.* delivery.
//
//	payment.completed → confirm the order (pending → confirmed)
//	payment.failed    → leave the order pending so the payment can be retried
func (c *PaymentConsumer) handle(ctx context.Context, d amqp.Delivery) error {
	var event struct {
		EventID     string `json:"event_id"`
		EventType   string `json:"event_type"`
		PaymentID   uint   `json:"payment_id"`
		OrderID     uint   `json:"order_id"`
		UserID      uint   `json:"user_id"`
		Amount      int64  `json:"amount"`
		Currency    string `json:"currency"`
		ProviderRef string `json:"provider_ref"`
		Reason      string `json:"reason"`
	}
	if err := json.Unmarshal(d.Body, &event); err != nil {
		return fmt.Errorf("unmarshal event: %w", err)
	}

	switch event.EventType {
	case "payment.completed":
		confirmed, err := c.store.ConfirmOrder(ctx, event.OrderID)
		if err != nil {
			// Transient failure (e.g. database blip) → retry/DLQ strategy.
			return fmt.Errorf("confirm order %d: %w", event.OrderID, err)
		}

		if !confirmed {
			telemetry.L(ctx).InfoContext(ctx, "payment already settled, order not pending",
				"payment_id", event.PaymentID,
				"order_id", event.OrderID,
				"event_id", event.EventID,
			)
			return nil
		}

		telemetry.L(ctx).InfoContext(ctx, "payment succeeded, order confirmed",
			"payment_id", event.PaymentID,
			"amount", event.Amount,
			"currency", event.Currency,
			"provider_ref", event.ProviderRef,
			"order_id", event.OrderID,
			"event_id", event.EventID,
		)

	case "payment.failed":
		// The order stays pending: the user can retry the payment.
		telemetry.L(ctx).WarnContext(ctx, "payment failed, order left pending",
			"payment_id", event.PaymentID,
			"order_id", event.OrderID,
			"reason", event.Reason,
			"event_id", event.EventID,
		)

	default:
		telemetry.L(ctx).WarnContext(ctx, "unknown payment event type",
			"event_type", event.EventType, "event_id", event.EventID)
	}

	return nil
}
