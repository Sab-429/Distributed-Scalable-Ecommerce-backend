package models

import "time"

// OutboxEventStatus tracks where an outbox row is in its lifecycle.
type OutboxEventStatus string

const (
	OutboxStatusPending   OutboxEventStatus = "pending"
	OutboxStatusPublished  OutboxEventStatus = "published"
)

// OutboxEvent is a row in the transactional outbox. It is written in the same
// DB transaction as the order/state change so that the order and its event are
// committed atomically. A background relay polls for pending rows, publishes
// them to RabbitMQ, and marks them published.
type OutboxEvent struct {
	ID uint `json:"id" gorm:"primaryKey"`

	// EventID is a unique UUID per event (becomes the RabbitMQ MessageId).
	EventID string `json:"event_id" gorm:"type:varchar(36);not null;uniqueIndex"`

	// EventType is the business event type, e.g. "order.created".
	EventType string `json:"event_type" gorm:"type:varchar(50);not null;index"`

	// AggregateID is the order ID this event belongs to.
	AggregateID uint `json:"aggregate_id" gorm:"not null;index"`

	// RoutingKey is the RabbitMQ routing key, e.g. "order.created".
	RoutingKey string `json:"routing_key" gorm:"type:varchar(50);not null"`

	// Payload is the JSON-encoded event body that will be published to RabbitMQ.
	Payload []byte `json:"payload" gorm:"type:bytea;not null"`

	// Status tracks whether the relay has published this event.
	Status OutboxEventStatus `json:"status" gorm:"type:varchar(20);not null;default:pending;index"`

	CreatedAt time.Time `json:"created_at" gorm:"not null;index"`

	PublishedAt *time.Time `json:"published_at" gorm:"index"`
}
