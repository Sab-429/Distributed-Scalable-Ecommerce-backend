package dto

import "time"

// UserResponse is the public representation of a user — no password hash.
type UserResponse struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// AuthResponse is returned after a successful login or token refresh.
type AuthResponse struct {
	AccessToken  string        `json:"access_token"`
	RefreshToken string        `json:"refresh_token"`
	TokenType    string        `json:"token_type"`
	ExpiresIn    int64         `json:"expires_in"` // seconds
	User         *UserResponse `json:"user"`
}

// RegisterResponse is returned after a successful registration.
type RegisterResponse struct {
	Message string        `json:"message"`
	User    *UserResponse `json:"user"`
}
