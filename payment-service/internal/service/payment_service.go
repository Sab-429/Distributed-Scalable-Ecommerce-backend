package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"payment-service/internal/dto"
	"payment-service/internal/gateway"
	"payment-service/internal/messaging"
	"payment-service/internal/models"
	"payment-service/internal/repository"

	"gorm.io/gorm"
)

var (
	ErrInvalidPayment      = errors.New("invalid payment")
	ErrPaymentNotFound     = errors.New("payment not found")
	ErrUnauthorizedPayment = errors.New("unauthorized payment")
	ErrAlreadyPaid         = errors.New("payment already succeeded")
	ErrPaymentCancelled    = errors.New("payment is cancelled")

	// ErrEventPublish marks a settled payment whose result event could not be
	// handed to RabbitMQ. The payment itself is persisted, so callers should
	// surface the payment and treat the notification as retryable.
	ErrEventPublish = errors.New("payment settled but event publish failed")
)

type PaymentService struct {
	repo          *repository.PaymentRepository
	publisher     *messaging.Publisher
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
		publisher:     publisher,
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
//	order.created   → create the payment (idempotent) and charge it
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
		log.Printf("[payments] ignoring unknown event type %q (event %s)", event.EventType, event.EventID)
		return nil
	}
}

func (s *PaymentService) handleOrderCreated(ctx context.Context, event messaging.OrderEvent) error {
	payment, err := s.repo.FindByOrderID(ctx, event.OrderID)
	switch {
	case err == nil:
		switch payment.Status {
		case models.PaymentStatusSucceeded:
			// Redelivered event: the payment already settled. Re-publish the
			// completion so the order-service is guaranteed to see it
			// (its status update is idempotent).
			return s.publishCompleted(ctx, payment)
		case models.PaymentStatusCancelled:
			log.Printf("[payments] order %d was cancelled — ignoring order.created (event %s)",
				event.OrderID, event.EventID)
			return nil
		default:
			// pending / processing / failed → (re)attempt the charge.
			return s.process(ctx, payment, payment.Method)
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
		log.Printf("[payments] created payment %d for order %d (amount %d %s, event %s)",
			payment.ID, payment.OrderID, payment.Amount, payment.Currency, event.EventID)

		return s.process(ctx, payment, payment.Method)

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
		log.Printf("[payments] cancelled %d payment(s) for cancelled order %d", affected, event.OrderID)
	}
	return nil
}

// ── Charging ─────────────────────────────────────────────────────────────────

// process drives a payment through the gateway and publishes the outcome.
// The payment row is always persisted before its event is published, so a
// crash never leaves an event without a matching database state.
func (s *PaymentService) process(ctx context.Context, payment *models.Payment, method string) error {
	if method == "" {
		method = s.defaultMethod
	}

	payment.Method = method
	payment.Status = models.PaymentStatusProcessing
	if err := s.repo.Update(ctx, payment); err != nil {
		return fmt.Errorf("mark payment %d processing: %w", payment.ID, err)
	}

	log.Printf("[payments] charging payment %d (order %d, %d %s via %s)",
		payment.ID, payment.OrderID, payment.Amount, payment.Currency, method)

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
			log.Printf("[payments] failed to persist gateway error for payment %d: %v", payment.ID, updErr)
		}
		return fmt.Errorf("charge payment %d: %w", payment.ID, err)
	}

	if !result.Success {
		payment.Status = models.PaymentStatusFailed
		payment.FailureReason = truncate(result.Message, 255)
		payment.ProviderRef = ""
		if err := s.repo.Update(ctx, payment); err != nil {
			return fmt.Errorf("mark payment %d failed: %w", payment.ID, err)
		}
		log.Printf("[payments] payment %d declined for order %d: %s",
			payment.ID, payment.OrderID, payment.FailureReason)

		return s.publishFailed(ctx, payment)
	}

	now := time.Now()
	payment.Status = models.PaymentStatusSucceeded
	payment.ProviderRef = result.ProviderRef
	payment.FailureReason = ""
	payment.PaidAt = &now
	if err := s.repo.Update(ctx, payment); err != nil {
		return fmt.Errorf("mark payment %d succeeded: %w", payment.ID, err)
	}
	log.Printf("[payments] payment %d succeeded for order %d (ref %s)",
		payment.ID, payment.OrderID, payment.ProviderRef)

	return s.publishCompleted(ctx, payment)
}

func (s *PaymentService) publishCompleted(ctx context.Context, payment *models.Payment) error {
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
	if err := s.publisher.PublishPaymentCompleted(ctx, event); err != nil {
		return fmt.Errorf("%w: %v", ErrEventPublish, err)
	}
	log.Printf("[messaging] published payment.completed for order %d (event %s)", payment.OrderID, event.EventID)
	return nil
}

func (s *PaymentService) publishFailed(ctx context.Context, payment *models.Payment) error {
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
	if err := s.publisher.PublishPaymentFailed(ctx, event); err != nil {
		return fmt.Errorf("%w: %v", ErrEventPublish, err)
	}
	log.Printf("[messaging] published payment.failed for order %d (event %s)", payment.OrderID, event.EventID)
	return nil
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

// Pay (re)charges an unsettled payment. It is the manual/retry counterpart of
// the automatic charge performed on order.created.
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
