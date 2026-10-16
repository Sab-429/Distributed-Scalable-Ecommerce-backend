package models

import "time"

type PaymentStatus string

const (
	PaymentStatusPending    PaymentStatus = "pending"
	PaymentStatusProcessing PaymentStatus = "processing"
	PaymentStatusSucceeded  PaymentStatus = "succeeded"
	PaymentStatusFailed     PaymentStatus = "failed"
	PaymentStatusCancelled  PaymentStatus = "cancelled"
)

// Payment is created from an order.created event (or manually via the API) and
// then charged through the payment gateway.
type Payment struct {
	ID uint `json:"id" gorm:"primaryKey"`

	// OrderID is unique: one payment attempt chain per order.
	OrderID uint `json:"order_id" gorm:"not null;uniqueIndex"`

	UserID uint `json:"user_id" gorm:"not null;index"`

	Amount int64 `json:"amount" gorm:"not null"`

	Currency string `json:"currency" gorm:"type:varchar(3);not null;default:INR"`

	Status PaymentStatus `json:"status" gorm:"type:varchar(30);not null;default:pending;index"`

	// Method is the payment instrument (card, upi, netbanking, ...).
	Method string `json:"method" gorm:"type:varchar(30);not null;default:card"`

	// ProviderRef is the reference returned by the gateway on success.
	ProviderRef string `json:"provider_ref,omitempty" gorm:"type:varchar(255)"`

	FailureReason string `json:"failure_reason,omitempty" gorm:"type:varchar(255)"`

	PaidAt *time.Time `json:"paid_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`

	UpdatedAt time.Time `json:"updated_at"`
}
