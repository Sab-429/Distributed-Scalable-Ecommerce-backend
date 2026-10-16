package handler

import (
	"errors"
	"net/http"
	"order-service/internal/dto"
	"order-service/internal/service"
	"strconv"

	"github.com/gin-gonic/gin"
)

type OrderHandler struct {
	service *service.OrderService
}

func NewOrderHandler(
	service *service.OrderService,
) *OrderHandler {
	return &OrderHandler{
		service: service,
	}
}

func (h *OrderHandler) CreateOrder(c *gin.Context) {
	var req dto.CreateOrderRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid request body",
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

	order, err := h.service.CreateOrder(
		c.Request.Context(),
		userID,
		req,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidOrder):
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "invalid order",
			})
		case errors.Is(err, service.ErrProductNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"error": "product not found",
			})
		case errors.Is(err, service.ErrInsufficientStock):
			c.JSON(http.StatusConflict, gin.H{
				"error": "insufficient stock",
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "failed to create order",
			})
		}
		return
	}

	c.JSON(http.StatusCreated, order)
}

func (h *OrderHandler) GetOrder(c *gin.Context) {
	orderID, err := parseID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid order id",
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

	order, err := h.service.GetOrder(
		c.Request.Context(),
		userID,
		orderID,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrOrderNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"error": "order not found",
			})
		case errors.Is(err, service.ErrUnauthorizedOrder):
			c.JSON(http.StatusForbidden, gin.H{
				"error": "you cannot access this order",
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "failed to fetch order",
			})
		}
		return
	}

	c.JSON(http.StatusOK, order)
}

func (h *OrderHandler) GetOrders(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized",
		})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	orders, err := h.service.GetUserOrder(
		c.Request.Context(),
		userID,
		limit,
		offset,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to fetch orders",
		})
		return
	}

	c.JSON(http.StatusOK, orders)
}

func (h *OrderHandler) CancelOrder(c *gin.Context) {
	orderID, err := parseID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid order id",
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

	err = h.service.CancelOrder(
		c.Request.Context(),
		userID,
		orderID,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrOrderNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"error": "order not found",
			})
		case errors.Is(err, service.ErrUnauthorizedOrder):
			c.JSON(http.StatusForbidden, gin.H{
				"error": "you cannot cancel this order",
			})
		case errors.Is(err, service.ErrOrderAlreadyClosed):
			c.JSON(http.StatusConflict, gin.H{
				"error": "order cannot be cancelled",
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "failed to cancel order",
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "order cancelled successfully",
	})
}

func (h *OrderHandler) CompleteOrder(c *gin.Context) {
	orderID, err := parseID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid order id",
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

	err = h.service.CompleteOrder(
		c.Request.Context(),
		userID,
		orderID,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrOrderNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"error": "order not found",
			})
		case errors.Is(err, service.ErrUnauthorizedOrder):
			c.JSON(http.StatusForbidden, gin.H{
				"error": "you cannot complete this order",
			})
		case errors.Is(err, service.ErrOrderAlreadyClosed):
			c.JSON(http.StatusConflict, gin.H{
				"error": "order cannot be completed",
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "failed to complete order",
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "order completed successfully",
	})
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
