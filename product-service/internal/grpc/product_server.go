package grpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	v1 "product-service/proto/product/v1"
	"product-service/internal/service"
)

// ProductServer implements v1.ProductServiceServer.
type ProductServer struct {
	v1.UnimplementedProductServiceServer
	svc *service.ProductService
}

// NewProductServer creates a new ProductServer backed by the given service.
func NewProductServer(svc *service.ProductService) *ProductServer {
	return &ProductServer{svc: svc}
}

// GetProduct handles the gRPC GetProduct RPC.
func (s *ProductServer) GetProduct(ctx context.Context, req *v1.GetProductRequest) (*v1.GetProductResponse, error) {
	// proto uses uint64; the service/repository layer uses uint.
	product, err := s.svc.GetProduct(uint(req.GetProductId()))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "product %d not found", req.GetProductId())
		}
		return nil, status.Errorf(codes.Internal, "failed to get product: %v", err)
	}

	return &v1.GetProductResponse{
		Product: &v1.Product{
			Id:          uint64(product.ID),
			Name:        product.Name,
			Description: product.Description,
			PriceMinor:  product.PriceMinor, // set by AfterFind GORM hook
			Currency:    "INR",              // TODO: store currency in the product model
			Stock:       int32(product.Stock),
			Category:    product.Category,
		},
	}, nil
}
