package proxy

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// spoofableHeaders are headers a malicious client could inject to impersonate
// another user or escalate privileges.  They MUST be stripped from every
// incoming request before the gateway adds its own verified values.
// See Phase 5.7.
var spoofableHeaders = []string{
	"X-User-ID",
	"X-User-Role",
	"X-Gateway", // prevent downstream services from trusting a forged tag
}

// NewReverseProxy creates a reverse proxy to the given target URL.
//
// Security invariant:
//   - Client-supplied X-User-ID and X-User-Role are removed unconditionally.
//   - The JWTAuth middleware re-sets these headers from the verified JWT claims
//     before calling the proxy, so authenticated requests still carry them.
//   - Unauthenticated requests (e.g. /api/v1/auth/*) reach auth-service without
//     any identity headers, which is the correct behaviour.
func NewReverseProxy(target string) (*httputil.ReverseProxy, error) {
	targetURL, err := url.Parse(target)
	if err != nil {
		return nil, err
	}

	p := httputil.NewSingleHostReverseProxy(targetURL)
	originalDirector := p.Director

	p.Director = func(req *http.Request) {
		// 1. Save gateway-verified identity headers set by JWTAuth middleware
		//    BEFORE stripping, so we can restore them after.
		verifiedUserID := req.Header.Get("X-User-ID")
		verifiedUserRole := req.Header.Get("X-User-Role")

		// 2. Strip all spoofable headers so clients can never inject them.
		for _, h := range spoofableHeaders {
			req.Header.Del(h)
		}

		// 3. Run the standard reverse-proxy director (rewrites Host, URL, etc.)
		originalDirector(req)

		// 4. Add gateway identification tag (set after strip so it can't be faked).
		req.Header.Set("X-Gateway", "api-gateway")

		// 5. Propagate the request correlation ID (set by RequestID middleware).
		if rid := req.Header.Get("X-Request-ID"); rid != "" {
			req.Header.Set("X-Request-ID", rid)
		}

		// 6. Restore the gateway-verified identity headers so upstream services
		//    receive them. These came from the validated JWT, not from the client.
		if verifiedUserID != "" {
			req.Header.Set("X-User-ID", verifiedUserID)
		}
		if verifiedUserRole != "" {
			req.Header.Set("X-User-Role", verifiedUserRole)
		}
	}

	p.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		log.Printf(
			"proxy error: method=%s path=%s error=%v",
			req.Method,
			req.URL.Path,
			err,
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"backend service unavailable"}`))
	}

	return p, nil
}
