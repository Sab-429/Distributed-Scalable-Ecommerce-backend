package messaging

const (
	// Exchange
	OrderEventsExchange = "order.events"

	// Routing keys
	OrderCreatedRoutingKey   = "order.created"
	OrderCancelledRoutingKey = "order.cancelled"
	OrderCompletedRoutingKey = "order.completed"

	// Main notification queue
	NotificationQueue = "notification.order.events"

	// Dead Letter Exchange
	OrderDLX = "order.events.dlx"

	// Dead Letter Queue
	OrderDLQ = "order.events.dead"

	// Retry exchange
	OrderRetryExchange = "order.events.retry"

	// Retry queue
	OrderRetryQueue = "notification.order.retry"

	// ── Payment events published by payment-service ───────────────────────────

	// Exchange
	PaymentEventsExchange = "payment.events"

	// Routing keys
	PaymentCompletedRoutingKey = "payment.completed"
	PaymentFailedRoutingKey    = "payment.failed"

	// Queue this service consumes payment events from
	OrderPaymentQueue = "order.payment.events"

	// Dead letter exchange
	OrderPaymentDLX = "order.payment.events.dlx"

	// Dead letter queue
	OrderPaymentDLQ = "order.payment.events.dead"

	// Retry exchange — deliberately separate from OrderRetryExchange so a
	// retried payment message never reaches the notification-worker.
	OrderPaymentRetryExchange = "order.payment.events.retry"

	// Retry queue
	OrderPaymentRetryQueue = "order.payment.retry"
)
