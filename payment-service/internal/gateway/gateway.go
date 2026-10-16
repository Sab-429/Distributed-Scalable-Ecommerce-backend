package gateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ChargeRequest describes one attempt to capture a payment.
type ChargeRequest struct {
	PaymentID uint
	OrderID   uint
	UserID    uint
	Amount    int64
	Currency  string
	Method    string
}

// ChargeResult is the business outcome of a charge. Success=false means the
// payment was declined (a normal outcome), whereas a non-nil error means the
// gateway itself could not be reached or failed unexpectedly.
type ChargeResult struct {
	Success     bool
	ProviderRef string
	Message     string
}

// Gateway is the payment-gateway port. Swap SimulatedGateway for a real
// provider (Stripe, Razorpay, ...) without touching the service layer.
type Gateway interface {
	Charge(ctx context.Context, req ChargeRequest) (*ChargeResult, error)
}

// SimulatedGateway approves every charge after a short delay, except for the
// reserved method "declined" which always fails — a deterministic hook for
// exercising the payment.failed path.
type SimulatedGateway struct {
	Latency time.Duration
}

// DeclinedMethod is the payment method that always gets declined.
const DeclinedMethod = "declined"

func NewSimulatedGateway(latency time.Duration) *SimulatedGateway {
	return &SimulatedGateway{Latency: latency}
}

func (g *SimulatedGateway) Charge(
	ctx context.Context,
	req ChargeRequest,
) (*ChargeResult, error) {
	if req.Amount <= 0 {
		return nil, errors.New("amount must be positive")
	}

	// Simulate gateway round-trip latency.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(g.Latency):
	}

	if req.Method == DeclinedMethod {
		return &ChargeResult{
			Success: false,
			Message: "card declined by issuer (simulated)",
		}, nil
	}

	return &ChargeResult{
		Success:     true,
		ProviderRef: fmt.Sprintf("mockpay_%s", uuid.NewString()),
		Message:     "approved (simulated)",
	}, nil
}
