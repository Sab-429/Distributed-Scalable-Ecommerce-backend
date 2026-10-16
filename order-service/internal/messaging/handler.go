package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
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
			log.Printf("[orders] payment %d settled for order %d but it is not pending — already %s (event %s)",
				event.PaymentID, event.OrderID, "handled", event.EventID)
			return nil
		}

		log.Printf("[orders] payment %d succeeded (%d %s, ref %s) → order %d confirmed (event %s)",
			event.PaymentID, event.Amount, event.Currency, event.ProviderRef, event.OrderID, event.EventID)

	case "payment.failed":
		// The order stays pending: the user can retry the payment.
		log.Printf("[orders] payment %d failed for order %d: %s (event %s) — order left pending",
			event.PaymentID, event.OrderID, event.Reason, event.EventID)

	default:
		log.Printf("[orders] unknown payment event type %q (event %s) — ignoring", event.EventType, event.EventID)
	}

	return nil
}
