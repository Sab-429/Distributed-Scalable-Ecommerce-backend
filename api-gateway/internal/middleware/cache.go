package middleware

import (
	"bytes"
	"log"
	"net/http"

	"api-gateway/internal/cache"

	"github.com/gin-gonic/gin"
)

// bodyWriter wraps gin.ResponseWriter so we can capture the response body
// as the reverse proxy writes it, then store it in Redis afterwards.
type bodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (bw *bodyWriter) Write(b []byte) (int, error) {
	bw.body.Write(b) // capture a copy
	return bw.ResponseWriter.Write(b)
}

// ResponseCache returns a Gin middleware that:
//  1. On a cache HIT  — serves the stored body immediately (200 OK) without
//     touching the upstream product-service.
//  2. On a cache MISS — lets the request pass through to the reverse proxy,
//     captures the response body, and stores it in Redis for future requests.
//
// Only 200 OK responses are cached; errors are never stored.
func ResponseCache(rc *cache.ResponseCache) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.RequestURI() // includes query string if any

		// ── Cache check ───────────────────────────────────────────────────
		cached, err := rc.Get(c.Request.Context(), path)
		if err != nil {
			// Redis error — log and fall through to upstream (fail-open).
			log.Printf("[cache] get error for %s: %v", path, err)
		}

		if cached != "" {
			log.Printf("[cache] HIT  %s", path)
			c.Header("Content-Type", "application/json")
			c.Header("X-Cache", "HIT")
			c.String(http.StatusOK, cached)
			c.Abort() // do not forward to proxy
			return
		}

		log.Printf("[cache] MISS %s", path)

		// ── Cache miss: intercept the proxy response ───────────────────────
		bw := &bodyWriter{
			ResponseWriter: c.Writer,
			body:           &bytes.Buffer{},
		}
		c.Writer = bw
		c.Header("X-Cache", "MISS")

		c.Next() // run the reverse proxy handler

		// Only cache successful responses.
		if bw.Status() == http.StatusOK && bw.body.Len() > 0 {
			if setErr := rc.Set(c.Request.Context(), path, bw.body.String()); setErr != nil {
				log.Printf("[cache] set error for %s: %v", path, setErr)
			}
		}
	}
}

// ResponseCacheInvalidate returns a middleware that deletes the cache entry
// for the request path after a successful mutation (PUT / DELETE).
// Attach this to PUT and DELETE product routes.
func ResponseCacheInvalidate(rc *cache.ResponseCache) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next() // let the proxy handle the mutation first

		// Only invalidate if the mutation succeeded.
		status := c.Writer.Status()
		if status >= 200 && status < 300 {
			path := c.Request.URL.RequestURI()
			if err := rc.Delete(c.Request.Context(), path); err != nil {
				log.Printf("[cache] invalidate error for %s: %v", path, err)
			} else {
				log.Printf("[cache] invalidated %s", path)
			}
		}
	}
}
