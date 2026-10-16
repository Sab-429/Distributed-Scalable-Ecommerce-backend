package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	redisclient "product-service/internal/redis"
	"product-service/internal/models"
)

const productTTL = 10 * time.Minute

// ProductCache handles caching of Product objects in Redis.
type ProductCache struct {
	client *redisclient.Client
}

// NewProductCache constructs a ProductCache backed by the given Redis client.
func NewProductCache(client *redisclient.Client) *ProductCache {
	return &ProductCache{client: client}
}

// Get retrieves a product from the cache.
// Returns (nil, nil) on a cache miss so callers can distinguish miss from error.
func (pc *ProductCache) Get(ctx context.Context, id uint) (*models.Product, error) {
	key := ProductKey(id)

	val, err := pc.client.Get(ctx, key)
	if err != nil {
		if errors.Is(err, redisclient.ErrNil) {
			// Cache miss — not an error
			return nil, nil
		}
		return nil, fmt.Errorf("cache get product %d: %w", id, err)
	}

	var product models.Product
	if err := json.Unmarshal([]byte(val), &product); err != nil {
		return nil, fmt.Errorf("cache unmarshal product %d: %w", id, err)
	}

	return &product, nil
}

// Set stores a product in the cache with the default TTL.
func (pc *ProductCache) Set(ctx context.Context, product *models.Product) error {
	data, err := json.Marshal(product)
	if err != nil {
		return fmt.Errorf("cache marshal product %d: %w", product.ID, err)
	}

	key := ProductKey(product.ID)
	if err := pc.client.Set(ctx, key, string(data), productTTL); err != nil {
		return fmt.Errorf("cache set product %d: %w", product.ID, err)
	}

	return nil
}

// Delete removes a product from the cache (called on update/delete).
func (pc *ProductCache) Delete(ctx context.Context, id uint) error {
	key := ProductKey(id)
	if err := pc.client.Delete(ctx, key); err != nil {
		return fmt.Errorf("cache delete product %d: %w", id, err)
	}
	return nil
}
