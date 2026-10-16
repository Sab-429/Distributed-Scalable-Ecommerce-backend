package dto

type CreateOrderRequest struct {
	Items []CreateOrderItem `json:"items" binding:"required,min=1"`
}

type CreateOrderItem struct {
	ProductID uint `json:"product_id" binding:"required"`
	Quantity int `json:"quantity" binding:"required,min=1"`
}

type CancelOrderRequest struct {
	Reason string `json:"reason"`
}