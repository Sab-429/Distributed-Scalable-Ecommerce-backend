package routes

import (
	"github.com/gin-gonic/gin"

	"order-service/internal/handler"
	"order-service/internal/middleware"
)

func Setup(
	router *gin.Engine,
	orderHandler *handler.OrderHandler,
) {

	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "ok",
			"service": "order-service",
		})
	})

	api := router.Group("/api/v1")

	orders := api.Group("/orders")

	orders.Use(
		middleware.UserContext(),
	)

	{
		orders.POST(
			"",
			orderHandler.CreateOrder,
		)

		orders.GET(
			"",
			orderHandler.GetOrders,
		)

		orders.GET(
			"/:id",
			orderHandler.GetOrder,
		)

		orders.PATCH(
			"/:id/cancel",
			orderHandler.CancelOrder,
		)

		orders.PATCH(
			"/:id/complete",
			orderHandler.CompleteOrder,
		)
	}
}