package clients

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "order-service/proto/product/v1"
)

// ProductClient wraps the gRPC ProductServiceClient.
type ProductClient struct {
	client pb.ProductServiceClient
	conn   *grpc.ClientConn
}

// NewProductClient dials the product-service gRPC server at the given address
// (e.g. "localhost:50051") and returns a ready-to-use client.
func NewProductClient(address string) (*ProductClient, error) {
	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
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

// Close releases the underlying gRPC connection.
func (c *ProductClient) Close() error {
	return c.conn.Close()
}
