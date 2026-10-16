package routes

import (
	"github.com/gin-gonic/gin"

	"payment-service/internal/handler"
	"payment-service/internal/middleware"
)

func Setup(
	router *gin.Engine,
	paymentHandler *handler.PaymentHandler,
) {

	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "ok",
			"service": "payment-service",
		})
	})

	api := router.Group("/api/v1")

	payments := api.Group("/payments")

	payments.Use(
		middleware.UserContext(),
	)

	{
		payments.GET(
			"",
			paymentHandler.GetPayments,
		)

		payments.GET(
			"/:id",
			paymentHandler.GetPayment,
		)

		payments.POST(
			"/:id/pay",
			paymentHandler.Pay,
		)
	}
}
