package consumer

// Topology — must match order-service/internal/messaging/topology.go.
const (
	// Exchange
	OrderEventsExchange = "order.events"

	// Routing keys
	OrderCreatedRoutingKey   = "order.created"
	OrderCancelledRoutingKey = "order.cancelled"
	OrderCompletedRoutingKey = "order.completed"

	// Main notification queue
	NotificationQueue = "notification.order.events"

	// Dead-letter exchange / queue
	OrderDLX = "order.events.dlx"
	OrderDLQ = "order.events.dead"

	// Retry exchange / queue
	OrderRetryExchange = "order.events.retry"
	OrderRetryQueue    = "notification.order.retry"
)

// maxRetries is the number of times a failing message is republished to the
// retry exchange before it is dead-lettered.
const maxRetries = 3
