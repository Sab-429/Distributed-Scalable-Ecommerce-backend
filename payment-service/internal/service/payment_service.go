package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"payment-service/internal/dto"
	"payment-service/internal/gateway"
	"payment-service/internal/messaging"
	"payment-service/internal/models"
	"payment-service/internal/repository"

	"observability"

	"gorm.io/gorm"
)

var (
	ErrInvalidPayment      = errors.New("invalid payment")
	ErrPaymentNotFound     = errors.New("payment not found")
	ErrUnauthorizedPayment = errors.New("unauthorized payment")
	ErrAlreadyPaid         = errors.New("payment already succeeded")
	ErrPaymentCancelled    = errors.New("payment is cancelled")
	ErrPaymentProcessing   = errors.New("payment is already processing")

	// ErrEventPublish marks a settled payment whose result event could not be
	// handed to RabbitMQ. The payment itself is persisted, so callers should
	// surface the payment and treat the notification as retryable.
	ErrEventPublish = errors.New("payment settled but event publish failed")
)

type PaymentService struct {
	repo          *repository.PaymentRepository
	gateway       gateway.Gateway
	defaultMethod string
}

func NewPaymentService(
	repo *repository.PaymentRepository,
	publisher *messaging.Publisher,
	gateway gateway.Gateway,
	defaultMethod string,
) *PaymentService {
	return &PaymentService{
		repo:          repo,
		gateway:       gateway,
		defaultMethod: defaultMethod,
	}
}

// Compile-time check: the messaging.Consumer calls this service through the
// messaging.EventHandler interface.
var _ messaging.EventHandler = (*PaymentService)(nil)

// ── RabbitMQ consumer entry point ────────────────────────────────────────────

// HandleOrderEvent reacts to order.* events published by the order-service.
//
//	order.created   → create the payment record as "pending" (no auto-charge)
//	order.cancelled → cancel a payment that has not settled yet
//	order.completed → nothing to do
func (s *PaymentService) HandleOrderEvent(ctx context.Context, event messaging.OrderEvent) error {
	switch event.EventType {
	case "order.created":
		return s.handleOrderCreated(ctx, event)
	case "order.cancelled":
		return s.handleOrderCancelled(ctx, event)
	case "order.completed":
		return nil
	default:
		telemetry.L(ctx).WarnContext(ctx, "ignoring unknown event type",
			"event_type", event.EventType, "event_id", event.EventID)
		return nil
	}
}

func (s *PaymentService) handleOrderCreated(ctx context.Context, event messaging.OrderEvent) error {
	payment, err := s.repo.FindByOrderID(ctx, event.OrderID)
	switch {
	case err == nil:
		switch payment.Status {
		case models.PaymentStatusSucceeded:
			// Redelivered event: the payment already settled. Write an
			// outbox event so the order-service is guaranteed to see it
			// (its status update is idempotent).
			outboxEvent, err := s.buildPaymentCompletedOutbox(payment)
			if err != nil {
				return err
			}
			if err := s.repo.UpdateWithOutbox(ctx, payment, outboxEvent); err != nil {
				return fmt.Errorf("republish payment.completed for order %d: %w", event.OrderID, err)
			}
			telemetry.L(ctx).InfoContext(ctx, "outbox queued payment.completed (redelivery)",
				"order_id", event.OrderID)
			return nil
		case models.PaymentStatusCancelled:
			telemetry.L(ctx).InfoContext(ctx, "order cancelled, ignoring order.created",
				"order_id", event.OrderID, "event_id", event.EventID)
			return nil
		case models.PaymentStatusProcessing:
			// A charge is already in-flight for this order (e.g. a redelivered
			// order.created arrived while the first process() call is still
			// talking to the gateway).  Re-charging would double-charge the
			// customer. Skip and let the in-flight charge complete.
			telemetry.L(ctx).WarnContext(ctx, "payment already processing, skipping redelivery",
				"payment_id", payment.ID, "order_id", event.OrderID, "event_id", event.EventID)
			return nil
		default:
			// pending / failed → payment exists but user hasn't paid yet.
			// Do NOT auto-charge on redelivery — the user must explicitly
			// initiate payment via POST /payments/:id/pay.
			telemetry.L(ctx).InfoContext(ctx, "payment exists, not auto-charging",
				"payment_id", payment.ID, "order_id", event.OrderID,
				"status", string(payment.Status), "event_id", event.EventID)
			return nil
		}

	case errors.Is(err, gorm.ErrRecordNotFound):
		payment = &models.Payment{
			OrderID:  event.OrderID,
			UserID:   event.UserID,
			Amount:   event.TotalAmount,
			Currency: currencyOrDefault(event.Currency),
			Status:   models.PaymentStatusPending,
			Method:   s.defaultMethod,
		}
		if err := s.repo.Create(ctx, payment); err != nil {
			return fmt.Errorf("create payment for order %d: %w", event.OrderID, err)
		}
		telemetry.L(ctx).InfoContext(ctx, "payment created, awaiting user to initiate",
			"payment_id", payment.ID, "order_id", payment.OrderID,
			"amount", payment.Amount, "currency", payment.Currency, "event_id", event.EventID)

		// Do NOT auto-charge. In a real e-commerce flow the user must
		// explicitly initiate payment via POST /payments/:id/pay. The payment
		// stays "pending" until then.
		return nil

	default:
		return fmt.Errorf("load payment for order %d: %w", event.OrderID, err)
	}
}

func (s *PaymentService) handleOrderCancelled(ctx context.Context, event messaging.OrderEvent) error {
	affected, err := s.repo.CancelPendingByOrderID(ctx, event.OrderID)
	if err != nil {
		return fmt.Errorf("cancel payments for order %d: %w", event.OrderID, err)
	}
	if affected > 0 {
		telemetry.L(ctx).InfoContext(ctx, "payments cancelled with order",
			"cancelled", affected, "order_id", event.OrderID)
	}
	return nil
}

// ── Charging ─────────────────────────────────────────────────────────────────

// process drives a payment through the gateway. The payment status change
// and its outbox event are written in the same DB transaction via
// repo.UpdateWithOutbox, so a crash never leaves the payment settled without
// a corresponding event. The outbox relay publishes the event to RabbitMQ
// asynchronously.
func (s *PaymentService) process(ctx context.Context, payment *models.Payment, method string) error {
	if method == "" {
		method = s.defaultMethod
	}

	payment.Method = method
	payment.Status = models.PaymentStatusProcessing
	if err := s.repo.Update(ctx, payment); err != nil {
		return fmt.Errorf("mark payment %d processing: %w", payment.ID, err)
	}

	telemetry.L(ctx).InfoContext(ctx, "charging payment",
		"payment_id", payment.ID, "order_id", payment.OrderID,
		"amount", payment.Amount, "currency", payment.Currency, "method", method)

	result, err := s.gateway.Charge(ctx, gateway.ChargeRequest{
		PaymentID: payment.ID,
		OrderID:   payment.OrderID,
		UserID:    payment.UserID,
		Amount:    payment.Amount,
		Currency:  payment.Currency,
		Method:    method,
	})
	if err != nil {
		// Gateway/system failure (not a decline): record it and let the caller
		// decide whether to retry — the consumer will retry the message.
		payment.Status = models.PaymentStatusFailed
		payment.FailureReason = truncate(err.Error(), 255)
		if updErr := s.repo.Update(ctx, payment); updErr != nil {
			telemetry.L(ctx).ErrorContext(ctx, "failed to persist gateway error",
				"payment_id", payment.ID, "error", updErr)
		}
		return fmt.Errorf("charge payment %d: %w", payment.ID, err)
	}

	if !result.Success {
		payment.Status = models.PaymentStatusFailed
		payment.FailureReason = truncate(result.Message, 255)
		payment.ProviderRef = ""

		// Build the payment.failed outbox event.
		outboxEvent, err := s.buildPaymentFailedOutbox(payment)
		if err != nil {
			return err
		}

		// Persist payment state + outbox event atomically.
		if err := s.repo.UpdateWithOutbox(ctx, payment, outboxEvent); err != nil {
			return fmt.Errorf("mark payment %d failed: %w", payment.ID, err)
		}
		telemetry.L(ctx).WarnContext(ctx, "payment declined",
			"payment_id", payment.ID, "order_id", payment.OrderID,
			"reason", payment.FailureReason)

		return nil
	}

	now := time.Now()
	payment.Status = models.PaymentStatusSucceeded
	payment.ProviderRef = result.ProviderRef
	payment.FailureReason = ""
	payment.PaidAt = &now

	// Build the payment.completed outbox event.
	outboxEvent, err := s.buildPaymentCompletedOutbox(payment)
	if err != nil {
		return err
	}

	// Persist payment state + outbox event atomically.
	if err := s.repo.UpdateWithOutbox(ctx, payment, outboxEvent); err != nil {
		return fmt.Errorf("mark payment %d succeeded: %w", payment.ID, err)
	}
	telemetry.L(ctx).InfoContext(ctx, "payment succeeded",
		"payment_id", payment.ID, "order_id", payment.OrderID,
		"provider_ref", payment.ProviderRef)

	return nil
}

// buildPaymentCompletedOutbox creates an outbox event for payment.completed.
func (s *PaymentService) buildPaymentCompletedOutbox(payment *models.Payment) (*models.OutboxEvent, error) {
	event := messaging.NewPaymentCompletedEvent(
		payment.ID,
		payment.OrderID,
		payment.UserID,
		payment.Amount,
		payment.Currency,
		payment.Method,
		payment.ProviderRef,
		paidAtOrDefault(payment),
	)

	payload, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal payment.completed event: %w", err)
	}

	return &models.OutboxEvent{
		EventID:     event.EventID,
		EventType:   event.EventType,
		AggregateID: payment.ID,
		RoutingKey:  messaging.PaymentCompletedRoutingKey,
		Payload:     payload,
		Status:      models.OutboxStatusPending,
		CreatedAt:   time.Now(),
	}, nil
}

// buildPaymentFailedOutbox creates an outbox event for payment.failed.
func (s *PaymentService) buildPaymentFailedOutbox(payment *models.Payment) (*models.OutboxEvent, error) {
	event := messaging.NewPaymentFailedEvent(
		payment.ID,
		payment.OrderID,
		payment.UserID,
		payment.Amount,
		payment.Currency,
		payment.Method,
		payment.FailureReason,
		time.Now(),
	)

	payload, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal payment.failed event: %w", err)
	}

	return &models.OutboxEvent{
		EventID:     event.EventID,
		EventType:   event.EventType,
		AggregateID: payment.ID,
		RoutingKey:  messaging.PaymentFailedRoutingKey,
		Payload:     payload,
		Status:      models.OutboxStatusPending,
		CreatedAt:   time.Now(),
	}, nil
}

// ── HTTP use cases ───────────────────────────────────────────────────────────

// GetPayment returns a payment the user owns.
func (s *PaymentService) GetPayment(
	ctx context.Context,
	userID uint,
	paymentID uint,
) (*models.Payment, error) {
	payment, err := s.repo.FindByID(ctx, paymentID)
	if err != nil {
		return nil, ErrPaymentNotFound
	}
	if payment.UserID != userID {
		return nil, ErrUnauthorizedPayment
	}
	return payment, nil
}

// GetUserPayments lists a user's payments, newest first.
func (s *PaymentService) GetUserPayments(
	ctx context.Context,
	userID uint,
	limit int,
	offset int,
) ([]models.Payment, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.FindByUserID(ctx, userID, limit, offset)
}

// Pay charges a payment that has not settled yet. This is the primary entry
// point for payment — the user explicitly initiates it via POST /payments/:id/pay.
// The order.created consumer only creates the payment record as "pending";
// it does NOT auto-charge. The user must call this endpoint to complete payment.
func (s *PaymentService) Pay(
	ctx context.Context,
	userID uint,
	paymentID uint,
	req dto.PayPaymentRequest,
) (*models.Payment, error) {
	payment, err := s.repo.FindByID(ctx, paymentID)
	if err != nil {
		return nil, ErrPaymentNotFound
	}
	if payment.UserID != userID {
		return nil, ErrUnauthorizedPayment
	}

	switch payment.Status {
	case models.PaymentStatusSucceeded:
		return payment, ErrAlreadyPaid
	case models.PaymentStatusCancelled:
		return payment, ErrPaymentCancelled
	case models.PaymentStatusProcessing:
		// A charge is already in-flight (e.g. the order.created consumer
		// is still talking to the gateway). Re-charging would double-charge.
		return payment, ErrPaymentProcessing
	}

	if err := s.process(ctx, payment, req.Method); err != nil {
		return payment, err
	}
	return payment, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func currencyOrDefault(currency string) string {
	if currency == "" {
		return "INR"
	}
	return currency
}

func paidAtOrDefault(payment *models.Payment) time.Time {
	if payment.PaidAt != nil {
		return *payment.PaidAt
	}
	return time.Now()
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
