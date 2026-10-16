package handler

import (
	"net/http"
	"product-service/internal/models"
	"product-service/internal/service"
	"strconv"

	"github.com/gin-gonic/gin"
)

type ProductHandler struct {
	service *service.ProductService
}

func NewProductHandler(
	service *service.ProductService,
) *ProductHandler {
	return &ProductHandler{
		service: service,
	}
}

func (h *ProductHandler) CreateProduct(c *gin.Context) {
	var product models.Product

	if err := c.ShouldBindJSON(&product); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request body",
		})
		return
	}

	err := h.service.CreateProduct(&product)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}
	c.JSON(http.StatusCreated, product)
}

func (h *ProductHandler) GetProduct(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid product id",
		})
		return
	}

	product, err := h.service.GetByID(c.Request.Context(), uint(id))

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "product not found",
		})
		return
	}
	c.JSON(http.StatusOK, product)
}

func (h *ProductHandler) GetProducts(c *gin.Context) {

	products, err := h.service.GetProducts()

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to fetch products",
		})
		return
	}
	c.JSON(http.StatusOK, products)
}

func (h *ProductHandler) UpdateProduct(c *gin.Context) {

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid product id",
		})
		return
	}
	var product models.Product

	if err := c.ShouldBindJSON(&product); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid request body",
		})
		return
	}
	product.ID = uint(id)

	err = h.service.UpdateProduct(c.Request.Context(), &product)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, product)
}

func (h *ProductHandler) DeleteProduct(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)

	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid product id",
		})
		return
	}

	err = h.service.DeleteProduct(c.Request.Context(), uint(id))

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to delete product",
		})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}
