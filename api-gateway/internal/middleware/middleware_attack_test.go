// Package middleware_test covers Phase 5.9 attack and failure scenarios for
// the API gateway JWT authentication and authorisation middleware.
//
// Scenarios tested:
//   - Missing JWT                         → 401
//   - Malformed / invalid JWT             → 401
//   - Expired JWT                         → 401
//   - Wrong signature (different secret)  → 401
//   - Wrong role (customer hits admin route) → 403
//   - Accessing another user's order      → 403 (via X-User-ID enforcement)
//   - Injected X-User-ID header stripped  → cannot spoof identity
//   - Injected X-User-Role header stripped → cannot escalate privileges
//   - Valid JWT passes through            → 200
package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"api-gateway/auth"
	"api-gateway/internal/middleware"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
)

// ── Test helpers ──────────────────────────────────────────────────────────────

const (
	testSecret   = "test-secret-key-for-unit-tests"
	testIssuer   = "auth-service"
	testAudience = "commerce-api"
)

// newTestParser creates a Parser using the test secret / issuer.
func newTestParser() *auth.Parser {
	return auth.NewParserWithConfig(testSecret, testIssuer, testAudience)
}

// makeToken generates a signed JWT with the given parameters.
func makeToken(
	secret string,
	userID uint,
	role string,
	issuer string,
	audience string,
	expiry time.Duration,
) string {
	now := time.Now()
	claims := auth.Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatUint(uint64(userID), 10),
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := token.SignedString([]byte(secret))
	return signed
}

// makeExpiredToken creates a token that expired 1 hour ago.
func makeExpiredToken(userID uint, role string) string {
	now := time.Now()
	claims := auth.Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatUint(uint64(userID), 10),
			Issuer:    testIssuer,
			Audience:  jwt.ClaimStrings{testAudience},
			ExpiresAt: jwt.NewNumericDate(now.Add(-1 * time.Hour)), // already expired
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
			NotBefore: jwt.NewNumericDate(now.Add(-2 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := token.SignedString([]byte(testSecret))
	return signed
}

// validToken returns a valid 24-hour token for the given user and role.
func validToken(userID uint, role string) string {
	return makeToken(testSecret, userID, role, testIssuer, testAudience, 24*time.Hour)
}

// routerWithJWT builds a minimal Gin engine with JWTAuth on GET /protected.
func routerWithJWT(parser *auth.Parser) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/protected", middleware.JWTAuth(parser), func(c *gin.Context) {
		uid, _ := c.Get(string(auth.ContextUserID))
		role, _ := c.Get(string(auth.ContextUserRole))
		c.JSON(http.StatusOK, gin.H{"user_id": uid, "role": role})
	})
	return r
}

// routerWithRBAC builds a Gin engine where GET /admin requires role "admin".
func routerWithRBAC(parser *auth.Parser) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin",
		middleware.JWTAuth(parser),
		middleware.RequireRoles("admin"),
		func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		},
	)
	return r
}

// doGet fires a GET request with an optional Authorization header.
func doGet(router *gin.Engine, path, authHeader string, extraHeaders map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// jsonError extracts the "error" field from a JSON response body.
func jsonError(t *testing.T, body string) string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return ""
	}
	return m["error"]
}

// ── 5.9.1  Missing JWT ────────────────────────────────────────────────────────

func TestJWTAuth_MissingToken(t *testing.T) {
	r := routerWithJWT(newTestParser())
	w := doGet(r, "/protected", "", nil)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if got := jsonError(t, w.Body.String()); got != "authorization header required" {
		t.Fatalf("unexpected error message: %q", got)
	}
}

// ── 5.9.2  Invalid JWT (garbage string) ──────────────────────────────────────

func TestJWTAuth_InvalidToken_Garbage(t *testing.T) {
	r := routerWithJWT(newTestParser())
	w := doGet(r, "/protected", "Bearer not.a.jwt", nil)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if got := jsonError(t, w.Body.String()); got != "invalid token" {
		t.Fatalf("unexpected error message: %q", got)
	}
}

// ── 5.9.3  Invalid JWT (wrong bearer format — no "Bearer " prefix) ────────────

func TestJWTAuth_MissingBearerPrefix(t *testing.T) {
	r := routerWithJWT(newTestParser())
	w := doGet(r, "/protected", validToken(1, "customer"), nil) // no "Bearer " prefix

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if got := jsonError(t, w.Body.String()); got != "bearer token required" {
		t.Fatalf("unexpected error message: %q", got)
	}
}

// ── 5.9.4  Expired JWT ────────────────────────────────────────────────────────

func TestJWTAuth_ExpiredToken(t *testing.T) {
	r := routerWithJWT(newTestParser())
	w := doGet(r, "/protected", "Bearer "+makeExpiredToken(1, "customer"), nil)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if got := jsonError(t, w.Body.String()); got != "token has expired" {
		t.Fatalf("unexpected error message: %q", got)
	}
}

// ── 5.9.5  Wrong signature (token signed with a different secret) ─────────────

func TestJWTAuth_WrongSignature(t *testing.T) {
	r := routerWithJWT(newTestParser())
	// Token signed with a completely different secret.
	badToken := makeToken("completely-different-secret", 1, "customer", testIssuer, testAudience, 24*time.Hour)
	w := doGet(r, "/protected", "Bearer "+badToken, nil)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if got := jsonError(t, w.Body.String()); got != "invalid token" {
		t.Fatalf("unexpected error message: %q", got)
	}
}

// ── 5.9.6  Wrong issuer ───────────────────────────────────────────────────────

func TestJWTAuth_WrongIssuer(t *testing.T) {
	r := routerWithJWT(newTestParser())
	badToken := makeToken(testSecret, 1, "customer", "evil-service", testAudience, 24*time.Hour)
	w := doGet(r, "/protected", "Bearer "+badToken, nil)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// ── 5.9.7  Wrong audience ─────────────────────────────────────────────────────

func TestJWTAuth_WrongAudience(t *testing.T) {
	r := routerWithJWT(newTestParser())
	badToken := makeToken(testSecret, 1, "customer", testIssuer, "other-api", 24*time.Hour)
	w := doGet(r, "/protected", "Bearer "+badToken, nil)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// ── 5.9.8  Wrong role (customer hits admin-only route) ────────────────────────

func TestRequireRoles_CustomerHitsAdminRoute(t *testing.T) {
	r := routerWithRBAC(newTestParser())
	token := validToken(42, "customer") // role = customer, not admin
	w := doGet(r, "/admin", "Bearer "+token, nil)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	if got := jsonError(t, w.Body.String()); got != "access denied: insufficient role" {
		t.Fatalf("unexpected error message: %q", got)
	}
}

// ── 5.9.9  Admin can access admin-only route ──────────────────────────────────

func TestRequireRoles_AdminPasses(t *testing.T) {
	r := routerWithRBAC(newTestParser())
	token := validToken(1, "admin")
	w := doGet(r, "/admin", "Bearer "+token, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// ── 5.9.10  Spoofed X-User-ID is stripped (5.7) ──────────────────────────────
// An attacker sends X-User-ID: 999 trying to impersonate user 999.
// The proxy strips it; JWTAuth re-sets it from the verified JWT (userID=1).
// The downstream handler (simulated here) must see userID=1, not 999.

func TestJWTAuth_SpoofedXUserIDIsOverwritten(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Simulate what the gateway does: JWTAuth sets context values and
	// re-writes X-User-ID on the request.
	r.GET("/orders", middleware.JWTAuth(newTestParser()), func(c *gin.Context) {
		// The downstream service reads X-User-ID from the forwarded header.
		// It should be the JWT-verified value, not the attacker's value.
		forwardedID := c.Request.Header.Get("X-User-ID")
		c.JSON(http.StatusOK, gin.H{"x_user_id": forwardedID})
	})

	token := validToken(1, "customer") // legitimate user is ID 1

	req := httptest.NewRequest(http.MethodGet, "/orders", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-User-ID", "999") // attacker tries to impersonate user 999

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	// The header seen by the downstream handler must be "1" (from JWT), not "999".
	if resp["x_user_id"] != "1" {
		t.Fatalf(
			"X-User-ID spoofing: downstream saw %q, expected \"1\"",
			resp["x_user_id"],
		)
	}
}

// ── 5.9.11  Spoofed X-User-Role is overwritten ───────────────────────────────
// An attacker sends X-User-Role: admin to try to escalate to admin.
// JWTAuth must overwrite this with the role from the verified JWT.

func TestJWTAuth_SpoofedXUserRoleIsOverwritten(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	r.GET("/check", middleware.JWTAuth(newTestParser()), func(c *gin.Context) {
		forwardedRole := c.Request.Header.Get("X-User-Role")
		c.JSON(http.StatusOK, gin.H{"x_user_role": forwardedRole})
	})

	token := validToken(1, "customer") // JWT encodes role=customer

	req := httptest.NewRequest(http.MethodGet, "/check", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-User-Role", "admin") // attacker tries to escalate

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	// Must be "customer" from the JWT, not "admin" from the attacker.
	if resp["x_user_role"] != "customer" {
		t.Fatalf(
			"X-User-Role spoofing: downstream saw %q, expected \"customer\"",
			resp["x_user_role"],
		)
	}
}

// ── 5.9.12  Accessing another user's order (5.6) ─────────────────────────────
// The order service enforces ownership by comparing X-User-ID (set from JWT)
// against order.UserID.  We simulate that check here to prove the contract.

func TestOrderOwnership_UserCannotAccessOtherUsersOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Simulate the order-service ownership check using the X-User-ID header
	// (as order-service/internal/middleware/user_context.go does).
	r.GET("/api/v1/orders/:id", middleware.JWTAuth(newTestParser()), func(c *gin.Context) {
		// Simulate order-service reading X-User-ID from the forwarded header.
		requestingUserID := c.Request.Header.Get("X-User-ID")

		// Hard-coded: this order belongs to user 99.
		orderOwnerID := "99"

		if requestingUserID != orderOwnerID {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "you cannot access this order",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"order_id": c.Param("id")})
	})

	// User 1 (customer) tries to access an order that belongs to user 99.
	token := validToken(1, "customer")
	w := doGet(r, "/api/v1/orders/42", "Bearer "+token, nil)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when accessing another user's order, got %d", w.Code)
	}
	if got := jsonError(t, w.Body.String()); got != "you cannot access this order" {
		t.Fatalf("unexpected error: %q", got)
	}
}

// ── 5.9.13  Valid JWT — happy path ────────────────────────────────────────────

func TestJWTAuth_ValidToken(t *testing.T) {
	r := routerWithJWT(newTestParser())
	token := validToken(42, "customer")
	w := doGet(r, "/protected", "Bearer "+token, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	// user_id is stored as uint but JSON decodes as float64.
	if uid, ok := resp["user_id"].(float64); !ok || uint(uid) != 42 {
		t.Fatalf("expected user_id=42, got %v", resp["user_id"])
	}
	if resp["role"] != "customer" {
		t.Fatalf("expected role=customer, got %v", resp["role"])
	}
}
