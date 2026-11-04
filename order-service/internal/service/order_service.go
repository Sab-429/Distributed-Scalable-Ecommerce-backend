package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"order-service/internal/clients"
	"order-service/internal/dto"
	"order-service/internal/messaging"
	"order-service/internal/models"
	"order-service/internal/repository"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrInvalidOrder              = errors.New("invalid order")
	ErrProductNotFound           = errors.New("product not found")
	ErrProductServiceUnavailable = errors.New("product service unavailable")
	ErrInsufficientStock         = errors.New("insufficient stock")
	ErrOrderNotFound             = errors.New("order not found")
	ErrUnauthorizedOrder         = errors.New("unauthorized order")
	ErrOrderAlreadyClosed        = errors.New("order already closed")
)

type OrderService struct {
	repo          *repository.OrderRepository
	productClient *clients.ProductClient
}

func NewOrderService(
	repo *repository.OrderRepository,
	productClient *clients.ProductClient,
) *OrderService {
	return &OrderService{
		repo:          repo,
		productClient: productClient,
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

	// Track successful stock reservations so we can roll them back if a
	// later item fails. Each entry records the product ID and quantity that
	// was successfully reserved via ReserveStock.
	type reservation struct {
		productID uint
		quantity  int
	}
	var reserved []reservation

	// releaseAll reverses every successful reservation. Called when order
	// creation fails after some items have already been reserved.
	releaseAll := func() {
		for _, r := range reserved {
			if err := s.productClient.ReleaseStock(ctx, r.productID, r.quantity); err != nil {
				log.Printf("[stock-rollback] failed to release %d units of product %d: %v",
					r.quantity, r.productID, err)
			} else {
				log.Printf("[stock-rollback] released %d units of product %d",
					r.quantity, r.productID)
			}
		}
	}

	for _, item := range req.Items {
		if item.Quantity <= 0 {
			releaseAll()
			return nil, ErrInvalidOrder
		}

		product, err := s.productClient.GetProduct(ctx, item.ProductID)
		if err != nil {
			// Inspect the gRPC status code to distinguish "product doesn't
			// exist" (404) from "product-service is down / timed out" (503).
			releaseAll()
			if st, ok := status.FromError(err); ok {
				switch st.Code() {
				case codes.NotFound:
					return nil, fmt.Errorf("%w: %v", ErrProductNotFound, err)
				default:
					return nil, fmt.Errorf("%w: %v", ErrProductServiceUnavailable, err)
				}
			}
			return nil, fmt.Errorf("%w: %v", ErrProductServiceUnavailable, err)
		}

		if int(product.GetStock()) < item.Quantity {
			releaseAll()
			return nil, ErrInsufficientStock
		}

		// Atomically reserve stock via gRPC to prevent concurrent overselling.
		success, err := s.productClient.ReserveStock(ctx, item.ProductID, item.Quantity)
		if err != nil {
			// gRPC-level error (service down, timeout, etc.)
			releaseAll()
			if st, ok := status.FromError(err); ok {
				switch st.Code() {
				case codes.NotFound:
					return nil, fmt.Errorf("%w: %v", ErrProductNotFound, err)
				default:
					return nil, fmt.Errorf("%w: %v", ErrProductServiceUnavailable, err)
				}
			}
			return nil, fmt.Errorf("%w: %v", ErrProductServiceUnavailable, err)
		}
		if !success {
			// Stock was sufficient at read time but the atomic reservation
			// failed — another order beat us to it.
			releaseAll()
			return nil, ErrInsufficientStock
		}

		// Record this reservation for potential rollback.
		reserved = append(reserved, reservation{
			productID: item.ProductID,
			quantity:  item.Quantity,
		})

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

	// ── Build the order.created event for the outbox ──────────────────────────
	//
	// The order ID isn't known until after the INSERT (auto-increment), but the
	// outbox event payload must contain it. CreateWithOutbox solves this by
	// accepting a callback that runs inside the transaction after the order
	// row is created — so the event gets the real ID.

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

	// Pre-generate the event UUID so it's available even though OrderID is
	// filled in by the callback.
	event := messaging.NewOrderCreatedEvent(
		0, // filled in by the callback after the order ID is known
		order.UserID,
		order.TotalAmount,
		order.Currency,
		itemEvents,
	)

	buildOutbox := func(created *models.Order) (*models.OutboxEvent, error) {
		event.OrderID = created.ID
		event.CreatedAt = time.Now()

		payload, err := json.Marshal(event)
		if err != nil {
			return nil, fmt.Errorf("marshal order.created event: %w", err)
		}

		return &models.OutboxEvent{
			EventID:     event.EventID,
			EventType:   event.EventType,
			AggregateID: created.ID,
			RoutingKey:  messaging.OrderCreatedRoutingKey,
			Payload:     payload,
			Status:      models.OutboxStatusPending,
			CreatedAt:   time.Now(),
		}, nil
	}

	if err := s.repo.CreateWithOutbox(ctx, order, buildOutbox); err != nil {
		// The DB transaction failed — release all reserved stock.
		releaseAll()
		return nil, err
	}

	log.Printf("[outbox] queued order.created for order %d (event %s)", order.ID, event.EventID)

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

	// ── Build the order.cancelled event for the outbox ──────────────────────
	event := messaging.NewOrderCancelledEvent(
		order.ID,
		order.UserID,
		order.TotalAmount,
		order.Currency,
		"", // reason — could be passed from the request in future
	)

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal order.cancelled event: %w", err)
	}

	outboxEvent := &models.OutboxEvent{
		EventID:     event.EventID,
		EventType:   event.EventType,
		AggregateID: order.ID,
		RoutingKey:  messaging.OrderCancelledRoutingKey,
		Payload:     payload,
		Status:      models.OutboxStatusPending,
		CreatedAt:   time.Now(),
	}

	if err := s.repo.UpdateStatusWithOutbox(
		ctx,
		orderID,
		models.OrderStatusCancelled,
		outboxEvent,
	); err != nil {
		return err
	}

	log.Printf("[outbox] queued order.cancelled for order %d (event %s)", order.ID, event.EventID)
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

	// ── Build the order.completed event for the outbox ──────────────────────
	event := messaging.NewOrderCompletedEvent(
		order.ID,
		order.UserID,
		order.TotalAmount,
		order.Currency,
	)

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal order.completed event: %w", err)
	}

	outboxEvent := &models.OutboxEvent{
		EventID:     event.EventID,
		EventType:   event.EventType,
		AggregateID: order.ID,
		RoutingKey:  messaging.OrderCompletedRoutingKey,
		Payload:     payload,
		Status:      models.OutboxStatusPending,
		CreatedAt:   time.Now(),
	}

	if err := s.repo.UpdateStatusWithOutbox(
		ctx,
		orderID,
		models.OrderStatusCompleted,
		outboxEvent,
	); err != nil {
		return err
	}

	log.Printf("[outbox] queued order.completed for order %d (event %s)", order.ID, event.EventID)
	return nil
}
