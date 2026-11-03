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

// Create persists the order, its items, and an outbox event in a single
// database transaction. If any write fails, the entire transaction rolls
// back — the order and its event are committed atomically.
func (r *OrderRepository) Create(
	ctx context.Context,
	order *models.Order,
	outboxEvent *models.OutboxEvent,
) error {
	return r.db.WithContext(ctx).Transaction(
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
			// Write the outbox event in the same transaction so the order
			// and its event are committed atomically.
			if outboxEvent != nil {
				if err := tx.Create(outboxEvent).Error; err != nil {
					return err
				}
			}
			return nil
		},
	)
}

// CreateWithOutbox persists the order and its items in a single transaction,
// then calls the buildOutbox callback with the freshly-created order (which
// now has its auto-increment ID set) so the caller can construct the outbox
// event with the correct order ID. The callback's returned outbox event is
// written in the same transaction.
func (r *OrderRepository) CreateWithOutbox(
	ctx context.Context,
	order *models.Order,
	buildOutbox func(*models.Order) (*models.OutboxEvent, error),
) error {
	return r.db.WithContext(ctx).Transaction(
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
			// Build the outbox event now that the order ID is known.
			if buildOutbox != nil {
				outboxEvent, err := buildOutbox(order)
				if err != nil {
					return err
				}
				if outboxEvent != nil {
					if err := tx.Create(outboxEvent).Error; err != nil {
						return err
					}
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

// UpdateStatusWithOutbox updates the order status and writes an outbox event
// in a single transaction. Used by CancelOrder and CompleteOrder.
func (r *OrderRepository) UpdateStatusWithOutbox(
	ctx context.Context,
	id uint,
	status models.OrderStatus,
	outboxEvent *models.OutboxEvent,
) error {
	return r.db.WithContext(ctx).Transaction(
		func(tx *gorm.DB) error {
			result := tx.Model(&models.Order{}).
				Where("id = ?", id).
				Update("status", status)

			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}

			if outboxEvent != nil {
				if err := tx.Create(outboxEvent).Error; err != nil {
					return err
				}
			}
			return nil
		},
	)
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
