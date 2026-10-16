package models

import "time"

type OrderItem struct {
	ID uint `json:"id" gorm:"primaryKey"`

	OrderID uint `json:"order_id" gorm:"not null;index"`

	ProductID uint `json:"product_id" gorm:"not null;index"`

	ProductName string `json:"product_name" gorm:"type:varchar(255);not null"`

	Quantity int `json:"quantity" gorm:"not null"`

	UnitPrice int64 `json:"unit_price" gorm:"not null"`

	Subtotal int64 `json:"subtotal" gorm:"not null"`

	CreatedAt time.Time `json:"created_at"`
}
