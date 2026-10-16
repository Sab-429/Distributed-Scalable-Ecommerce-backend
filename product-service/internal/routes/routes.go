package routes

import (
	"product-service/internal/handler"

	"github.com/gin-gonic/gin"
)

func SetupRoutes(
	router *gin.Engine,
	handler *handler.ProductHandler,
) {

	products := router.Group("/api/v1/products")
	{
		products.POST("", handler.CreateProduct)
		products.GET("", handler.GetProducts)
		products.GET("/:id", handler.GetProduct)
		products.PUT("/:id", handler.UpdateProduct)
		products.DELETE("/:id", handler.DeleteProduct)
	}
}