package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	redisclient "api-gateway/internal/redis"
)

const responseTTL = 5 * time.Minute

// ResponseCache stores raw HTTP response bodies keyed by request path.
// It sits in the api-gateway so repeated GET requests for the same product
// are served from Redis without hitting the upstream product-service at all.
type ResponseCache struct {
	client *redisclient.Client
}

// NewResponseCache constructs a ResponseCache backed by the given Redis client.
func NewResponseCache(client *redisclient.Client) *ResponseCache {
	return &ResponseCache{client: client}
}

// responseKey builds the Redis key for a given URL path.
// e.g. "/api/v1/products/4" → "gw:response:/api/v1/products/4"
// The "gw:response:" prefix namespaces gateway cache keys away from any keys
// that product-service may write to the same Redis instance.
func responseKey(path string) string {
	return fmt.Sprintf("gw:response:%s", path)
}

// Get returns the cached response body for the given path.
// Returns ("", nil) on a cache miss so callers can distinguish miss from error.
func (rc *ResponseCache) Get(ctx context.Context, path string) (string, error) {
	val, err := rc.client.Get(ctx, responseKey(path))
	if err != nil {
		if errors.Is(err, redisclient.ErrNil) {
			return "", nil // cache miss — not an error
		}
		return "", fmt.Errorf("response cache get %q: %w", path, err)
	}
	return val, nil
}

// Set stores a response body for the given path with the default TTL.
func (rc *ResponseCache) Set(ctx context.Context, path, body string) error {
	if err := rc.client.Set(ctx, responseKey(path), body, responseTTL); err != nil {
		return fmt.Errorf("response cache set %q: %w", path, err)
	}
	return nil
}

// Delete removes the cached response for the given path.
// Call this on successful PUT / DELETE so the next GET is always fresh.
func (rc *ResponseCache) Delete(ctx context.Context, path string) error {
	if err := rc.client.Delete(ctx, responseKey(path)); err != nil {
		return fmt.Errorf("response cache delete %q: %w", path, err)
	}
	return nil
}
