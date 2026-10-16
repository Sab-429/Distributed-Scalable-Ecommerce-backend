package messaging

import (
	"time"

	"github.com/google/uuid"
)

// OrderEvent is the shared envelope of every order.* event consumed from the
// order.events exchange.
type OrderEvent struct {
	EventID     string    `json:"event_id"`
	EventType   string    `json:"event_type"`
	OrderID     uint      `json:"order_id"`
	UserID      uint      `json:"user_id"`
	TotalAmount int64     `json:"total_amount"`
	Currency    string    `json:"currency"`
	CreatedAt   time.Time `json:"created_at"`
}

// PaymentCompletedEvent is published when a payment succeeds.
type PaymentCompletedEvent struct {
	EventID     string    `json:"event_id"`
	EventType   string    `json:"event_type"`
	PaymentID   uint      `json:"payment_id"`
	OrderID     uint      `json:"order_id"`
	UserID      uint      `json:"user_id"`
	Amount      int64     `json:"amount"`
	Currency    string    `json:"currency"`
	Method      string    `json:"method"`
	ProviderRef string    `json:"provider_ref"`
	PaidAt      time.Time `json:"paid_at"`
}

// PaymentFailedEvent is published when a payment is declined.
type PaymentFailedEvent struct {
	EventID   string    `json:"event_id"`
	EventType string    `json:"event_type"`
	PaymentID uint      `json:"payment_id"`
	OrderID   uint      `json:"order_id"`
	UserID    uint      `json:"user_id"`
	Amount    int64     `json:"amount"`
	Currency  string    `json:"currency"`
	Method    string    `json:"method"`
	Reason    string    `json:"reason"`
	FailedAt  time.Time `json:"failed_at"`
}

// NewPaymentCompletedEvent constructs a PaymentCompletedEvent with a UUID.
func NewPaymentCompletedEvent(
	paymentID, orderID, userID uint,
	amount int64,
	currency, method, providerRef string,
	paidAt time.Time,
) PaymentCompletedEvent {
	return PaymentCompletedEvent{
		EventID:     uuid.NewString(),
		EventType:   "payment.completed",
		PaymentID:   paymentID,
		OrderID:     orderID,
		UserID:      userID,
		Amount:      amount,
		Currency:    currency,
		Method:      method,
		ProviderRef: providerRef,
		PaidAt:      paidAt,
	}
}

// NewPaymentFailedEvent constructs a PaymentFailedEvent with a UUID.
func NewPaymentFailedEvent(
	paymentID, orderID, userID uint,
	amount int64,
	currency, method, reason string,
	failedAt time.Time,
) PaymentFailedEvent {
	return PaymentFailedEvent{
		EventID:   uuid.NewString(),
		EventType: "payment.failed",
		PaymentID: paymentID,
		OrderID:   orderID,
		UserID:    userID,
		Amount:    amount,
		Currency:  currency,
		Method:    method,
		Reason:    reason,
		FailedAt:  failedAt,
	}
}
