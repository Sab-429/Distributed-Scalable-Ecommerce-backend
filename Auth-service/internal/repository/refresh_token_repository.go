package repository

import (
	"Auth-service/internal/models"
	"errors"
	"time"

	"gorm.io/gorm"
)

// RefreshTokenRepository handles all DB operations for the refresh_tokens table.
type RefreshTokenRepository struct {
	db *gorm.DB
}

func NewRefreshTokenRepository(db *gorm.DB) *RefreshTokenRepository {
	return &RefreshTokenRepository{db: db}
}

// Create inserts a new refresh token record.
func (r *RefreshTokenRepository) Create(token *models.RefreshToken) error {
	return r.db.Create(token).Error
}

// FindByTokenHash returns a valid (non-expired) refresh token by its hash.
// Returns nil, nil if no matching token is found.
func (r *RefreshTokenRepository) FindByTokenHash(hash string) (*models.RefreshToken, error) {
	var token models.RefreshToken

	result := r.db.
		Where("token_hash = ? AND expires_at > ?", hash, time.Now()).
		First(&token)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return &token, nil
}

// DeleteByTokenHash removes a specific refresh token (used during logout).
func (r *RefreshTokenRepository) DeleteByTokenHash(hash string) error {
	return r.db.
		Where("token_hash = ?", hash).
		Delete(&models.RefreshToken{}).Error
}

// DeleteAllForUser removes every refresh token belonging to a user
// (used for "logout everywhere").
func (r *RefreshTokenRepository) DeleteAllForUser(userID uint) error {
	return r.db.
		Where("user_id = ?", userID).
		Delete(&models.RefreshToken{}).Error
}

// DeleteExpired removes all tokens that have passed their expiry time.
// Intended to be called from a background cleanup goroutine.
func (r *RefreshTokenRepository) DeleteExpired() error {
	return r.db.
		Where("expires_at <= ?", time.Now()).
		Delete(&models.RefreshToken{}).Error
}
