package middleware

import (
	"fmt"
	"net/http"

	"api-gateway/auth"

	"github.com/gin-gonic/gin"
)

// RequireRoles returns a middleware that aborts with 403 if the authenticated
// user does not have one of the specified roles.
//
// Must be used AFTER JWTAuth, which sets the "user_role" key in context.
//
// Usage:
//
//	adminRoutes.Use(middleware.RequireRoles("admin"))
//	staffRoutes.Use(middleware.RequireRoles("admin", "staff"))
func RequireRoles(allowedRoles ...string) gin.HandlerFunc {
	// Build a set for O(1) lookup.
	allowed := make(map[string]struct{}, len(allowedRoles))
	for _, r := range allowedRoles {
		allowed[r] = struct{}{}
	}

	return func(c *gin.Context) {
		roleVal, exists := c.Get(string(auth.ContextUserRole))
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "unauthorized",
			})
			c.Abort()
			return
		}

		role, ok := roleVal.(string)
		if !ok || role == "" {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "access denied",
			})
			c.Abort()
			return
		}

		if _, permitted := allowed[role]; !permitted {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "access denied: insufficient role",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireOwnerOrRoles allows the request to proceed only if the authenticated
// user either owns the resource (userID matches the URL param named by
// ownerParam) or has one of the privileged roles.
//
// This is useful for endpoints like GET /api/v1/orders/:userID where a regular
// user may only see their own orders but an admin can see any order.
//
// Must be used AFTER JWTAuth.
func RequireOwnerOrRoles(ownerParam string, privilegedRoles ...string) gin.HandlerFunc {
	privileged := make(map[string]struct{}, len(privilegedRoles))
	for _, r := range privilegedRoles {
		privileged[r] = struct{}{}
	}

	return func(c *gin.Context) {
		roleVal, _ := c.Get(string(auth.ContextUserRole))
		role, _ := roleVal.(string)

		// Privileged roles bypass ownership check.
		if _, ok := privileged[role]; ok {
			c.Next()
			return
		}

		// Regular users may only access their own resources.
		idVal, _ := c.Get(string(auth.ContextUserID))
		userID, _ := idVal.(uint)

		paramVal := c.Param(ownerParam)
		if paramVal == "" {
			c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
			c.Abort()
			return
		}

		// Allow numeric or string comparison.
		var paramIDStr string
		if len(paramVal) > 0 && paramVal[0] == '/' {
			paramIDStr = paramVal[1:] // strip leading slash from wildcard params
		} else {
			paramIDStr = paramVal
		}

		var ownerMatch bool

		// Try uint comparison first.
		if userID != 0 {
			var paramUint uint
			if n, err := fmt.Sscanf(paramIDStr, "%d", &paramUint); n == 1 && err == nil {
				ownerMatch = userID == paramUint
			}
		}

		if !ownerMatch {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "access denied: you can only access your own resources",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
