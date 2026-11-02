package models

import (
	"time"

	"gorm.io/gorm"
)

type Product struct {
	ID          uint      `json:"id" gorm:"primaryKey"`
	Name        string    `json:"name" gorm:"not null"`
	Description string    `json:"description"`
	Price       float64   `json:"price" gorm:"not null"`
	PriceMinor  int64     `json:"price_minor" gorm:"-"`
	Stock       int       `json:"stock" gorm:"not null"`
	Category    string    `json:"category" gorm:"not null"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"` 
}
func (p *Product) AfterFind(tx *gorm.DB) error {
	p.PriceMinor = int64(p.Price * 100)
	return nil
}
