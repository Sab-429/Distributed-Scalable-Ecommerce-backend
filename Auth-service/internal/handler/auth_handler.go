package handler

import (
	"Auth-service/internal/dto"
	"Auth-service/internal/service"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// AuthHandler exposes authentication endpoints.
type AuthHandler struct {
	authSvc *service.AuthService
}

func NewAuthHandler(authSvc *service.AuthService) *AuthHandler {
	return &AuthHandler{authSvc: authSvc}
}

// POST /api/v1/auth/register
func (h *AuthHandler) Register(c *gin.Context) {
	var req dto.RegisterRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	resp, err := h.authSvc.Register(req)
	if err != nil {
		h.handleServiceError(c, err)
		return
	}

	c.JSON(http.StatusCreated, resp)
}

// POST /api/v1/auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req dto.LoginRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email and password are required"})
		return
	}

	resp, err := h.authSvc.Login(req)
	if err != nil {
		h.handleServiceError(c, err)
		return
	}

	c.JSON(http.StatusOK, resp)
}

// POST /api/v1/auth/refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req dto.RefreshRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "refresh_token is required"})
		return
	}

	resp, err := h.authSvc.RefreshTokens(req)
	if err != nil {
		h.handleServiceError(c, err)
		return
	}

	c.JSON(http.StatusOK, resp)
}

// POST /api/v1/auth/logout
// Requires Authorization: Bearer <access_token>
func (h *AuthHandler) Logout(c *gin.Context) {
	var req dto.LogoutRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "refresh_token is required"})
		return
	}

	if err := h.authSvc.Logout(req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "logout failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "logged out successfully"})
}

// POST /api/v1/auth/logout-all
// Requires Authorization: Bearer <access_token>
// Revokes every refresh token for the authenticated user.
func (h *AuthHandler) LogoutAll(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if err := h.authSvc.LogoutAll(userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "logout failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "logged out from all devices"})
}

// GET /api/v1/auth/me
// Requires Authorization: Bearer <access_token>
func (h *AuthHandler) Me(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	resp, err := h.authSvc.Me(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch profile"})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// getUserID reads the user ID set by the JWT middleware from the Gin context.
// The key "user_id" is written by the auth middleware in routes.go.
func getUserID(c *gin.Context) (uint, bool) {
	val, exists := c.Get("user_id")
	if !exists {
		return 0, false
	}
	id, ok := val.(uint)
	return id, ok
}

// handleServiceError maps service-layer errors to HTTP responses.
func (h *AuthHandler) handleServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrEmailAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"error": "email already registered"})

	case errors.Is(err, service.ErrInvalidName):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})

	case errors.Is(err, service.ErrInvalidEmail):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid email address"})

	case errors.Is(err, service.ErrWeakPassword):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})

	case errors.Is(err, service.ErrInvalidCredentials):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid email or password"})

	case errors.Is(err, service.ErrInvalidToken):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})

	case errors.Is(err, service.ErrExpiredToken):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "token has expired"})

	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}
