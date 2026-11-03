package repository

import (
	"context"
	"fmt"
	"time"

	"order-service/internal/models"

	"gorm.io/gorm"
)

// OutboxRepository reads and updates outbox rows. Create operations happen
// inside the order transaction itself (see OrderRepository), so this struct
// only needs the methods the relay uses: GetPending and MarkPublished.
type OutboxRepository struct {
	db *gorm.DB
}

func NewOutboxRepository(db *gorm.DB) *OutboxRepository {
	return &OutboxRepository{db: db}
}

// GetPending returns up to limit outbox rows that are still pending, ordered
// by creation time so older events are published first. It uses FOR UPDATE
// SKIP LOCKED so multiple relay instances won't grab the same rows.
func (r *OutboxRepository) GetPending(ctx context.Context, limit int) ([]models.OutboxEvent, error) {
	var events []models.OutboxEvent

	err := r.db.WithContext(ctx).
		Raw(`
			SELECT * FROM outbox_events
			WHERE status = ?
			ORDER BY created_at ASC
			LIMIT ?
			FOR UPDATE SKIP LOCKED
		`, string(models.OutboxStatusPending), limit).
		Scan(&events).Error

	if err != nil {
		return nil, fmt.Errorf("get pending outbox events: %w", err)
	}
	return events, nil
}

// MarkPublished marks the given event as published with the current timestamp.
// It is called after the relay has confirmed that RabbitMQ received the message.
func (r *OutboxRepository) MarkPublished(ctx context.Context, eventID uint) error {
	now := time.Now()

	result := r.db.WithContext(ctx).
		Model(&models.OutboxEvent{}).
		Where("id = ?", eventID).
		Updates(map[string]interface{}{
			"status":       models.OutboxStatusPublished,
			"published_at": now,
		})

	if result.Error != nil {
		return fmt.Errorf("mark outbox event %d published: %w", eventID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("outbox event %d not found", eventID)
	}
	return nil
}
