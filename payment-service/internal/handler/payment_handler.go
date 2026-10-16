package handler

import (
	"errors"
	"log"
	"net/http"
	"payment-service/internal/dto"
	"payment-service/internal/service"
	"strconv"

	"github.com/gin-gonic/gin"
)

type PaymentHandler struct {
	service *service.PaymentService
}

func NewPaymentHandler(
	service *service.PaymentService,
) *PaymentHandler {
	return &PaymentHandler{
		service: service,
	}
}

// GetPayments lists the current user's payments.
func (h *PaymentHandler) GetPayments(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized",
		})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	payments, err := h.service.GetUserPayments(
		c.Request.Context(),
		userID,
		limit,
		offset,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to fetch payments",
		})
		return
	}

	c.JSON(http.StatusOK, payments)
}

// GetPayment returns a single payment owned by the current user.
func (h *PaymentHandler) GetPayment(c *gin.Context) {
	paymentID, err := parseID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid payment id",
		})
		return
	}

	userID, err := getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized",
		})
		return
	}

	payment, err := h.service.GetPayment(
		c.Request.Context(),
		userID,
		paymentID,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrPaymentNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"error": "payment not found",
			})
		case errors.Is(err, service.ErrUnauthorizedPayment):
			c.JSON(http.StatusForbidden, gin.H{
				"error": "you cannot access this payment",
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "failed to fetch payment",
			})
		}
		return
	}

	c.JSON(http.StatusOK, payment)
}

// Pay charges a payment that has not settled yet (retry-friendly).
func (h *PaymentHandler) Pay(c *gin.Context) {
	paymentID, err := parseID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid payment id",
		})
		return
	}

	userID, err := getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized",
		})
		return
	}

	// The body is optional — an empty body means "use the default method".
	var req dto.PayPaymentRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid request body",
			})
			return
		}
	}

	payment, err := h.service.Pay(
		c.Request.Context(),
		userID,
		paymentID,
		req,
	)
	if err != nil {
		// The payment settled but payment.completed could not reach RabbitMQ:
		// the money move is durable, so report success and let ops retry the
		// notification instead of failing the request.
		if payment != nil && errors.Is(err, service.ErrEventPublish) {
			log.Printf("[payments] payment %d settled but event publish failed: %v", payment.ID, err)
			c.JSON(http.StatusOK, payment)
			return
		}

		switch {
		case errors.Is(err, service.ErrPaymentNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"error": "payment not found",
			})
		case errors.Is(err, service.ErrUnauthorizedPayment):
			c.JSON(http.StatusForbidden, gin.H{
				"error": "you cannot access this payment",
			})
		case errors.Is(err, service.ErrAlreadyPaid):
			c.JSON(http.StatusConflict, gin.H{
				"error": "payment already succeeded",
			})
		case errors.Is(err, service.ErrPaymentCancelled):
			c.JSON(http.StatusConflict, gin.H{
				"error": "payment is cancelled",
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "failed to process payment",
			})
		}
		return
	}

	c.JSON(http.StatusOK, payment)
}

func parseID(value string) (uint, error) {
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 {
		return 0, errors.New("invalid id")
	}
	return uint(id), nil
}

func getUserID(c *gin.Context) (uint, error) {
	val, exists := c.Get("user_id")
	if !exists {
		return 0, errors.New("user_id not found in context")
	}
	id, ok := val.(uint)
	if !ok {
		return 0, errors.New("user_id is invalid type")
	}
	return id, nil
}
