package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrNil is returned by Get when a key does not exist (cache miss).
// Callers can compare with errors.Is(err, ErrNil) without importing go-redis.
var ErrNil = redis.Nil

// Client wraps go-redis so the rest of the app never imports go-redis directly.
type Client struct {
	rdb *redis.Client
}

// New creates a connected Redis client. It pings Redis before returning so a
// missing/unreachable Redis instance is caught at startup.
func New(addr string) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis: ping failed: %w", err)
	}

	return &Client{rdb: rdb}, nil
}

// Get retrieves the value stored at key.
// Returns ("", ErrNil) on a cache miss so callers can use errors.Is(err, ErrNil)
// without importing go-redis directly.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	val, err := c.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrNil
	}
	return val, err
}

// Set stores value at key with an optional TTL (0 = no expiry).
func (c *Client) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, value, ttl).Err()
}

// Delete removes one or more keys.
func (c *Client) Delete(ctx context.Context, keys ...string) error {
	return c.rdb.Del(ctx, keys...).Err()
}

// Close cleanly shuts down the connection pool.
func (c *Client) Close() error {
	return c.rdb.Close()
}
