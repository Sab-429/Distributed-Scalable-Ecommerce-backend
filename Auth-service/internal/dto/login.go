package dto

// LoginRequest is the body accepted by POST /api/v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// RefreshRequest is the body accepted by POST /api/v1/auth/refresh.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// LogoutRequest is the body accepted by POST /api/v1/auth/logout.
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}
