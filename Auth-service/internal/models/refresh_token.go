package models

import "time"

// RefreshToken stores long-lived tokens used to issue new access tokens.
// Each token is a cryptographically random string stored as a SHA-256 hash.
// Only the hash is persisted; the plaintext is returned to the client once.
type RefreshToken struct {
	ID        uint      `json:"id"         gorm:"primaryKey"`
	UserID    uint      `json:"user_id"    gorm:"not null;index"`
	TokenHash string    `json:"-"          gorm:"not null;unique"`
	ExpiresAt time.Time `json:"expires_at" gorm:"not null"`
	CreatedAt time.Time `json:"created_at"`

	// Belongs-to relationship — loaded on demand.
	User User `json:"-" gorm:"foreignKey:UserID"`
}
