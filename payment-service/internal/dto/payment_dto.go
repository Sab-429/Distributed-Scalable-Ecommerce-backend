package dto

type PayPaymentRequest struct {
	// Method is the payment instrument to charge with (card, upi, ...).
	// Use "declined" to exercise the simulated-decline path.
	Method string `json:"method"`
}
