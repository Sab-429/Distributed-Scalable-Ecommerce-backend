package models

import "time"

type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusConfirmed OrderStatus = "confirmed"
	OrderStatusCancelled OrderStatus = "cancelled"
	OrderStatusCompleted OrderStatus = "completed"
)

type Order struct {
	ID uint `json:"id" gorm:"primaryKey"`

	UserID uint `json:"user_id" gorm:"not null;index"`

	Status OrderStatus `json:"status" gorm:"type:varchar(30);not null;default:pending;index"`

	TotalAmount int64 `json:"total_amount" gorm:"not null"`

	Currency string `json:"currency" gorm:"type:varchar(3);not null;default:INR"`

	Items []OrderItem `json:"items" gorm:"foreignKey:OrderID;constraint:OnDelete:CASCADE"`

	CreatedAt time.Time `json:"created_at"`

	UpdatedAt time.Time `json:"updated_at"`
}
