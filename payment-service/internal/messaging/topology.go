package messaging

// Topology owned by the payment-service.
//
// The order.* names MUST match order-service/internal/messaging/topology.go so
// both services declare identical entities on the shared broker.
const (
	// ── Exchange the order-service publishes order.* events to ────────────────
	OrderEventsExchange = "order.events"

	// ── Exchange the payment-service publishes payment.* events to ────────────
	PaymentEventsExchange      = "payment.events"
	PaymentCompletedRoutingKey = "payment.completed"
	PaymentFailedRoutingKey    = "payment.failed"

	// ── Queue this service consumes order events from ─────────────────────────
	PaymentOrderQueue = "payment.order.events"

	// ── Dead-letter exchange / queue for payment.order.events ─────────────────
	PaymentOrderDLX = "payment.order.events.dlx"
	PaymentOrderDLQ = "payment.order.events.dead"

	// ── Retry exchange / queue for payment.order.events ───────────────────────
	// The retry queue dead-letters back into order.events (preserving the
	// original routing key) after 30s, so the message re-enters the consumer
	// queue. It is deliberately NOT shared with the notification-worker's
	// retry exchange — a retried message must only reach this consumer again.
	PaymentOrderRetryExchange = "payment.order.events.retry"
	PaymentOrderRetryQueue    = "payment.order.retry"
)
