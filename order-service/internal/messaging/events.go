package messaging

import (
	"time"

	"github.com/google/uuid"
)

// OrderItemEvent is the per-line-item payload included in order events.
type OrderItemEvent struct {
	ProductID   uint   `json:"product_id"`
	ProductName string `json:"product_name"`
	Quantity    int    `json:"quantity"`
	UnitPrice   int64  `json:"unit_price"`
	Subtotal    int64  `json:"subtotal"`
}

// OrderCreatedEvent is published when a new order is created.
type OrderCreatedEvent struct {
	EventID     string           `json:"event_id"`
	EventType   string           `json:"event_type"`
	OrderID     uint             `json:"order_id"`
	UserID      uint             `json:"user_id"`
	TotalAmount int64            `json:"total_amount"`
	Currency    string           `json:"currency"`
	Items       []OrderItemEvent `json:"items"`
	CreatedAt   time.Time        `json:"created_at"`
}

// OrderCancelledEvent is published when an order is cancelled.
type OrderCancelledEvent struct {
	EventID     string    `json:"event_id"`
	EventType   string    `json:"event_type"`
	OrderID     uint      `json:"order_id"`
	UserID      uint      `json:"user_id"`
	TotalAmount int64     `json:"total_amount"`
	Currency    string    `json:"currency"`
	Reason      string    `json:"reason"`
	CancelledAt time.Time `json:"cancelled_at"`
}

// NewOrderCreatedEvent constructs an OrderCreatedEvent with a generated UUID.
func NewOrderCreatedEvent(orderID, userID uint, totalAmount int64, currency string, items []OrderItemEvent) OrderCreatedEvent {
	return OrderCreatedEvent{
		EventID:     uuid.NewString(),
		EventType:   "order.created",
		OrderID:     orderID,
		UserID:      userID,
		TotalAmount: totalAmount,
		Currency:    currency,
		Items:       items,
		CreatedAt:   time.Now(),
	}
}

// OrderCompletedEvent is published when an order is completed.
type OrderCompletedEvent struct {
	EventID     string    `json:"event_id"`
	EventType   string    `json:"event_type"`
	OrderID     uint      `json:"order_id"`
	UserID      uint      `json:"user_id"`
	TotalAmount int64     `json:"total_amount"`
	Currency    string    `json:"currency"`
	CompletedAt time.Time `json:"completed_at"`
}

// NewOrderCompletedEvent constructs an OrderCompletedEvent with a generated UUID.
func NewOrderCompletedEvent(orderID, userID uint, totalAmount int64, currency string) OrderCompletedEvent {
	return OrderCompletedEvent{
		EventID:     uuid.NewString(),
		EventType:   "order.completed",
		OrderID:     orderID,
		UserID:      userID,
		TotalAmount: totalAmount,
		Currency:    currency,
		CompletedAt: time.Now(),
	}
}

// NewOrderCancelledEvent constructs an OrderCancelledEvent with a generated UUID.
func NewOrderCancelledEvent(orderID, userID uint, totalAmount int64, currency, reason string) OrderCancelledEvent {
	return OrderCancelledEvent{
		EventID:     uuid.NewString(),
		EventType:   "order.cancelled",
		OrderID:     orderID,
		UserID:      userID,
		TotalAmount: totalAmount,
		Currency:    currency,
		Reason:      reason,
		CancelledAt: time.Now(),
	}
}
