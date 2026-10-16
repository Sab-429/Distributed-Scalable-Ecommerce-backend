package middleware

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type clientLimiter struct {
	limiter  *rate.Limiter
	lastSeen int64
}

type RateLimiter struct {
	mu      sync.Mutex
	clients map[string]*clientLimiter

	rate  rate.Limit
	burst int
}

func NewRateLimiter(
	r rate.Limit,
	burst int,
) *RateLimiter {

	return &RateLimiter{
		clients: make(map[string]*clientLimiter),
		rate:    r,
		burst:   burst,
	}
}

func (rl *RateLimiter) Middleware() gin.HandlerFunc {

	return func(c *gin.Context) {

		ip := c.ClientIP()

		rl.mu.Lock()

		client, exists := rl.clients[ip]

		if !exists {
			client = &clientLimiter{
				limiter: rate.NewLimiter(
					rl.rate,
					rl.burst,
				),
			}

			rl.clients[ip] = client
		}

		allowed := client.limiter.Allow()

		rl.mu.Unlock()

		if !allowed {

			c.JSON(
				http.StatusTooManyRequests,
				gin.H{
					"error": "rate limit exceeded",
				},
			)

			c.Abort()

			return
		}

		c.Next()
	}
}