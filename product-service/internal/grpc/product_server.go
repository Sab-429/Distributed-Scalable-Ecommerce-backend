package grpc

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	v1 "product-service/proto/product/v1"
	"product-service/internal/repository"
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

// ReserveStock handles the gRPC ReserveStock RPC. It atomically decrements
// stock for the given product, preventing concurrent orders from overselling.
func (s *ProductServer) ReserveStock(ctx context.Context, req *v1.ReserveStockRequest) (*v1.ReserveStockResponse, error) {
	remaining, err := s.svc.ReserveStock(ctx, uint(req.GetProductId()), int(req.GetQuantity()))
	if err != nil {
		if errors.Is(err, repository.ErrProductNotFound) {
			return nil, status.Errorf(codes.NotFound, "product %d not found", req.GetProductId())
		}
		if errors.Is(err, repository.ErrInsufficientStock) {
			return &v1.ReserveStockResponse{
				Success:        false,
				RemainingStock: int32(remaining),
				Message:        fmt.Sprintf("insufficient stock for product %d: %d available", req.GetProductId(), remaining),
			}, nil
		}
		return nil, status.Errorf(codes.Internal, "failed to reserve stock: %v", err)
	}

	return &v1.ReserveStockResponse{
		Success:        true,
		RemainingStock: int32(remaining),
		Message:        fmt.Sprintf("reserved %d units of product %d", req.GetQuantity(), req.GetProductId()),
	}, nil
}

// ReleaseStock handles the gRPC ReleaseStock RPC. It atomically increments
// stock back for the given product, used to roll back a reservation when
// order creation fails.
func (s *ProductServer) ReleaseStock(ctx context.Context, req *v1.ReleaseStockRequest) (*v1.ReleaseStockResponse, error) {
	remaining, err := s.svc.ReleaseStock(ctx, uint(req.GetProductId()), int(req.GetQuantity()))
	if err != nil {
		if errors.Is(err, repository.ErrProductNotFound) {
			return nil, status.Errorf(codes.NotFound, "product %d not found", req.GetProductId())
		}
		return nil, status.Errorf(codes.Internal, "failed to release stock: %v", err)
	}

	return &v1.ReleaseStockResponse{
		RemainingStock: int32(remaining),
		Message:        fmt.Sprintf("released %d units of product %d", req.GetQuantity(), req.GetProductId()),
	}, nil
}
