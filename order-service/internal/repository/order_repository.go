package repository

import (
	"context"
	"order-service/internal/models"

	"gorm.io/gorm"
)

type OrderRepository struct {
	db *gorm.DB
}

func NewOrderRepository(db *gorm.DB) *OrderRepository {
	return &OrderRepository{
		db: db,
	}
}

//order Transaction

func (r *OrderRepository) Create(
	cntxt context.Context,
	order *models.Order,
) error {
	return r.db.WithContext(cntxt).Transaction(
		func(tx *gorm.DB) error {
			if err := tx.Omit("Items").Create(order).Error; err != nil {
				return err
			}
			for i := range order.Items {
				order.Items[i].OrderID = order.ID
				if err := tx.Create(&order.Items[i]).Error; err != nil {
					return err
				}
			}
			return nil
		},
	)
}

//Get Order

func (r *OrderRepository) FindByID(
	ctx context.Context,
	id uint,
) (*models.Order, error) {
	var order models.Order

	err := r.db.WithContext(ctx).Preload("Items").First(&order, id).Error

	if err != nil {
		return nil, err
	}
	return &order, nil
}

//Get user's orders

func (r *OrderRepository) FindByUserID(
	ctx context.Context,
	userID uint,
	limit int,
	offset int,
) ([]models.Order, error) {
	var orders []models.Order

	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Preload("Items").Order("created_at DESC").Limit(limit).Offset(offset).Find(&orders).Error

	if err != nil {
		return nil, err
	}
	return orders, nil
}

func (r *OrderRepository) UpdateStatus(
	ctx context.Context,
	id uint,
	status models.OrderStatus,
) error {
	result := r.db.WithContext(ctx).Model(&models.Order{}).Where("id = ?", id).Update("status", status)

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ConfirmOrder moves a pending order to confirmed once its payment succeeded.
//
// The UPDATE is guarded on status = 'pending', so a redelivered payment event
// (or an order that was cancelled/completed meanwhile) is a no-op: it reports
// false instead of overwriting a terminal state.
func (r *OrderRepository) ConfirmOrder(
	ctx context.Context,
	id uint,
) (bool, error) {
	result := r.db.WithContext(ctx).
		Model(&models.Order{}).
		Where("id = ? AND status = ?", id, models.OrderStatusPending).
		Update("status", models.OrderStatusConfirmed)

	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}
