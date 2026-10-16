package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"order-service/internal/clients"
	"order-service/internal/dto"
	"order-service/internal/messaging"
	"order-service/internal/models"
	"order-service/internal/repository"
)

var (
	ErrInvalidOrder       = errors.New("invalid order")
	ErrProductNotFound    = errors.New("product not found")
	ErrInsufficientStock  = errors.New("insufficient stock")
	ErrOrderNotFound      = errors.New("order not found")
	ErrUnauthorizedOrder  = errors.New("unauthorized order")
	ErrOrderAlreadyClosed = errors.New("order already closed")
)

type OrderService struct {
	repo          *repository.OrderRepository
	productClient *clients.ProductClient
	publisher     *messaging.Publisher
}

func NewOrderService(
	repo *repository.OrderRepository,
	productClient *clients.ProductClient,
	publisher *messaging.Publisher,
) *OrderService {
	return &OrderService{
		repo:          repo,
		productClient: productClient,
		publisher:     publisher,
	}
}

func (s *OrderService) CreateOrder(
	ctx context.Context,
	userID uint,
	req dto.CreateOrderRequest,
) (*models.Order, error) {
	if userID == 0 {
		return nil, ErrInvalidOrder
	}
	if len(req.Items) == 0 {
		return nil, ErrInvalidOrder
	}

	order := &models.Order{
		UserID:   userID,
		Status:   models.OrderStatusPending,
		Currency: "INR",
	}

	var total int64

	for _, item := range req.Items {
		if item.Quantity <= 0 {
			return nil, ErrInvalidOrder
		}

		product, err := s.productClient.GetProduct(ctx, item.ProductID)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrProductNotFound, err)
		}

		if int(product.GetStock()) < item.Quantity {
			return nil, ErrInsufficientStock
		}

		unitPrice := product.GetPriceMinor()
		subtotal := unitPrice * int64(item.Quantity)
		total += subtotal

		order.Items = append(order.Items, models.OrderItem{
			ProductID:   uint(product.GetId()),
			ProductName: product.GetName(),
			Quantity:    item.Quantity,
			UnitPrice:   unitPrice,
			Subtotal:    subtotal,
		})
	}

	order.TotalAmount = total

	if err := s.repo.Create(ctx, order); err != nil {
		return nil, err
	}

	// ── Publish order.created event ─────────────────────────────────────────
	itemEvents := make([]messaging.OrderItemEvent, len(order.Items))
	for i, item := range order.Items {
		itemEvents[i] = messaging.OrderItemEvent{
			ProductID:   item.ProductID,
			ProductName: item.ProductName,
			Quantity:    item.Quantity,
			UnitPrice:   item.UnitPrice,
			Subtotal:    item.Subtotal,
		}
	}

	event := messaging.NewOrderCreatedEvent(
		order.ID,
		order.UserID,
		order.TotalAmount,
		order.Currency,
		itemEvents,
	)

	if err := s.publisher.PublishOrderCreated(ctx, event); err != nil {
		// Log the error but don't fail the order — it's already persisted.
		log.Printf("[messaging] failed to publish order.created for order %d: %v", order.ID, err)
	} else {
		log.Printf("[messaging] published order.created for order %d (event %s)", order.ID, event.EventID)
	}

	return order, nil
}

func (s *OrderService) GetOrder(
	ctx context.Context,
	userID uint,
	orderID uint,
) (*models.Order, error) {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return nil, ErrOrderNotFound
	}
	if order.UserID != userID {
		return nil, ErrUnauthorizedOrder
	}
	return order, nil
}

func (s *OrderService) GetUserOrder(
	ctx context.Context,
	userID uint,
	limit int,
	offset int,
) ([]models.Order, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.FindByUserID(ctx, userID, limit, offset)
}

func (s *OrderService) CancelOrder(
	ctx context.Context,
	userID uint,
	orderID uint,
) error {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return ErrOrderNotFound
	}
	if order.UserID != userID {
		return ErrUnauthorizedOrder
	}
	if order.Status == models.OrderStatusCancelled || order.Status == models.OrderStatusCompleted {
		return ErrOrderAlreadyClosed
	}

	if err := s.repo.UpdateStatus(ctx, orderID, models.OrderStatusCancelled); err != nil {
		return err
	}

	// ── Publish order.cancelled event ───────────────────────────────────────
	event := messaging.NewOrderCancelledEvent(
		order.ID,
		order.UserID,
		order.TotalAmount,
		order.Currency,
		"", // reason — could be passed from the request in future
	)

	if err := s.publisher.PublishOrderCancelled(ctx, event); err != nil {
		log.Printf("[messaging] failed to publish order.cancelled for order %d: %v", order.ID, err)
	} else {
		log.Printf("[messaging] published order.cancelled for order %d (event %s)", order.ID, event.EventID)
	}

	return nil
}

func (s *OrderService) CompleteOrder(
	ctx context.Context,
	userID uint,
	orderID uint,
) error {
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return ErrOrderNotFound
	}
	if order.UserID != userID {
		return ErrUnauthorizedOrder
	}
	if order.Status == models.OrderStatusCancelled || order.Status == models.OrderStatusCompleted {
		return ErrOrderAlreadyClosed
	}

	if err := s.repo.UpdateStatus(ctx, orderID, models.OrderStatusCompleted); err != nil {
		return err
	}

	// ── Publish order.completed event ───────────────────────────────────────
	event := messaging.NewOrderCompletedEvent(
		order.ID,
		order.UserID,
		order.TotalAmount,
		order.Currency,
	)

	if err := s.publisher.PublishOrderCompleted(ctx, event); err != nil {
		log.Printf("[messaging] failed to publish order.completed for order %d: %v", order.ID, err)
	} else {
		log.Printf("[messaging] published order.completed for order %d (event %s)", order.ID, event.EventID)
	}

	return nil
}
