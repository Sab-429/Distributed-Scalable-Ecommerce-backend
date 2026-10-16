package repository

import (
	"Auth-service/internal/models"
	"errors"

	"gorm.io/gorm"
)

// UserRepository handles all DB operations for the users table.
type UserRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

// Create inserts a new user record.
func (r *UserRepository) Create(user *models.User) error {
	return r.db.Create(user).Error
}

// FindByEmail returns the user with the given email, or nil if not found.
func (r *UserRepository) FindByEmail(email string) (*models.User, error) {
	var user models.User

	result := r.db.Where("email = ?", email).First(&user)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return &user, nil
}

// FindByID returns the user with the given primary key, or nil if not found.
func (r *UserRepository) FindByID(id uint) (*models.User, error) {
	var user models.User

	result := r.db.First(&user, id)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return &user, nil
}
