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
	// PriceMinor is Price expressed in the smallest currency unit (paise/cents).
	// Computed from Price on every read; not persisted as its own column.
	PriceMinor  int64     `json:"price_minor" gorm:"-"`
	Stock       int       `json:"stock" gorm:"not null"`
	Category    string    `json:"category" gorm:"not null"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"` // fixed typo: was "upadted_at"
}

// AfterFind is a GORM hook called after every SELECT.
// It converts Price (float64 rupees) → PriceMinor (int64 paise).
func (p *Product) AfterFind(tx *gorm.DB) error {
	p.PriceMinor = int64(p.Price * 100)
	return nil
}
