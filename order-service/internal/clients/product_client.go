package clients

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "order-service/proto/product/v1"

	"observability"
)

// ProductClient wraps the gRPC ProductServiceClient.
type ProductClient struct {
	client pb.ProductServiceClient
	conn   *grpc.ClientConn
}

// NewProductClient dials the product-service gRPC server at the given address
// (e.g. "localhost:50051") and returns a ready-to-use client.
func NewProductClient(address string) (*ProductClient, error) {
	// The OTel stats handler traces every RPC and injects the W3C trace
	// context into the gRPC metadata so product-service joins this trace.
	conn, err := grpc.NewClient(
		address,
		append(
			telemetry.GRPCClientDialOptions(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to product service: %w", err)
	}

	return &ProductClient{
		client: pb.NewProductServiceClient(conn),
		conn:   conn,
	}, nil
}

// GetProduct fetches a product by ID from the product-service via gRPC.
// Returns the proto-generated *pb.Product directly so callers can use getters.
func (c *ProductClient) GetProduct(ctx context.Context, productID uint) (*pb.Product, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	resp, err := c.client.GetProduct(ctx, &pb.GetProductRequest{
		ProductId: uint64(productID),
	})
	if err != nil {
		return nil, fmt.Errorf("product service GetProduct: %w", err)
	}

	return resp.GetProduct(), nil
}

// ReserveStock atomically decrements the stock of the given product by the
// given quantity via the product-service gRPC RPC. Returns true if the
// reservation succeeded, false (with a message) if stock was insufficient.
func (c *ProductClient) ReserveStock(ctx context.Context, productID uint, quantity int) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	resp, err := c.client.ReserveStock(ctx, &pb.ReserveStockRequest{
		ProductId: uint64(productID),
		Quantity:  int32(quantity),
	})
	if err != nil {
		return false, fmt.Errorf("product service ReserveStock: %w", err)
	}

	return resp.GetSuccess(), nil
}

// ReleaseStock atomically increments the stock of the given product by the
// given quantity, rolling back a previous reservation. Used when order
// creation fails after some items have already been reserved.
func (c *ProductClient) ReleaseStock(ctx context.Context, productID uint, quantity int) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	_, err := c.client.ReleaseStock(ctx, &pb.ReleaseStockRequest{
		ProductId: uint64(productID),
		Quantity:  int32(quantity),
	})
	if err != nil {
		return fmt.Errorf("product service ReleaseStock: %w", err)
	}

	return nil
}

// Close releases the underlying gRPC connection.
func (c *ProductClient) Close() error {
	return c.conn.Close()
}
