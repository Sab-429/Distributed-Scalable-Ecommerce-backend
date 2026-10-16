package repository

import (
	"context"
	"payment-service/internal/models"

	"gorm.io/gorm"
)

type PaymentRepository struct {
	db *gorm.DB
}

func NewPaymentRepository(db *gorm.DB) *PaymentRepository {
	return &PaymentRepository{
		db: db,
	}
}

// Create persists a new payment.
func (r *PaymentRepository) Create(
	ctx context.Context,
	payment *models.Payment,
) error {
	return r.db.WithContext(ctx).Create(payment).Error
}

// FindByID returns a payment by its id.
func (r *PaymentRepository) FindByID(
	ctx context.Context,
	id uint,
) (*models.Payment, error) {
	var payment models.Payment

	err := r.db.WithContext(ctx).First(&payment, id).Error
	if err != nil {
		return nil, err
	}
	return &payment, nil
}

// FindByOrderID returns the payment attached to an order. OrderID is unique,
// so at most one record exists. Returns gorm.ErrRecordNotFound when the
// order-created event has not been processed yet.
func (r *PaymentRepository) FindByOrderID(
	ctx context.Context,
	orderID uint,
) (*models.Payment, error) {
	var payment models.Payment

	err := r.db.WithContext(ctx).Where("order_id = ?", orderID).First(&payment).Error
	if err != nil {
		return nil, err
	}
	return &payment, nil
}

// FindByUserID returns a user's payments, newest first.
func (r *PaymentRepository) FindByUserID(
	ctx context.Context,
	userID uint,
	limit int,
	offset int,
) ([]models.Payment, error) {
	var payments []models.Payment

	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&payments).Error
	if err != nil {
		return nil, err
	}
	return payments, nil
}

// Update writes every changed field of the payment back to the database.
func (r *PaymentRepository) Update(
	ctx context.Context,
	payment *models.Payment,
) error {
	return r.db.WithContext(ctx).Save(payment).Error
}

// CancelPendingByOrderID marks non-settled payments of a cancelled order as
// cancelled. It returns the number of rows updated.
func (r *PaymentRepository) CancelPendingByOrderID(
	ctx context.Context,
	orderID uint,
) (int64, error) {
	result := r.db.WithContext(ctx).
		Model(&models.Payment{}).
		Where(
			"order_id = ? AND status IN ?",
			orderID,
			[]models.PaymentStatus{
				models.PaymentStatusPending,
				models.PaymentStatusProcessing,
				models.PaymentStatusFailed,
			},
		).
		Update("status", models.PaymentStatusCancelled)

	return result.RowsAffected, result.Error
}
