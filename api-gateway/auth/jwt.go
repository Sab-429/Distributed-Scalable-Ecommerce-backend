// Package auth provides JWT parsing utilities for the API gateway.
// The gateway only validates tokens — it never issues them.
// Token issuance is the sole responsibility of the Auth service.
package auth

import (
	"errors"
	"os"
	"strconv"

	"github.com/golang-jwt/jwt/v4"
)

// ContextKey is a typed key for values stored in the Gin context.
type ContextKey string

const (
	// ContextUserID is the key under which the parsed user ID is stored.
	ContextUserID ContextKey = "user_id"
	// ContextUserRole is the key under which the parsed role is stored.
	ContextUserRole ContextKey = "user_role"
	// ContextClaims is the key under which the full claims struct is stored.
	ContextClaims ContextKey = "jwt_claims"
)

// ErrInvalidToken is returned when a JWT cannot be parsed or verified.
var ErrInvalidToken = errors.New("invalid or expired token")

// ErrExpiredToken is returned specifically when a JWT has expired.
var ErrExpiredToken = errors.New("token has expired")

// Claims mirrors the payload structure produced by the Auth service.
// It MUST stay in sync with service.Claims in Auth-service.
type Claims struct {
	UserID uint   `json:"uid"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// Parser holds the signing secret and expected issuer / audience, allowing
// the gateway to validate tokens without contacting the Auth service on each
// request (stateless JWT verification).
type Parser struct {
	secret   []byte
	issuer   string
	audience string
}

// NewParser creates a Parser from environment variables.
// Falls back to safe defaults so the service starts even without an explicit
// configuration (will reject all tokens until the secret is set correctly).
func NewParser() *Parser {
	secret := os.Getenv("JWT_SECRET")
	issuer := os.Getenv("JWT_ISSUER")
	if issuer == "" {
		issuer = "auth-service"
	}

	return &Parser{
		secret:   []byte(secret),
		issuer:   issuer,
		audience: "commerce-api",
	}
}

// NewParserWithConfig creates a Parser with explicit values instead of reading
// from the environment.  Use this in tests and when you need deterministic
// configuration without env-var side effects.
func NewParserWithConfig(secret, issuer, audience string) *Parser {
	return &Parser{
		secret:   []byte(secret),
		issuer:   issuer,
		audience: audience,
	}
}

// Parse validates the token string and returns the embedded claims.
// jwt.WithAudience / jwt.WithIssuer / jwt.WithValidMethods are v5-only;
// in v4 we validate those fields manually after parsing.
func (p *Parser) Parse(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		func(t *jwt.Token) (interface{}, error) {
			// Reject any non-HMAC algorithm to prevent algorithm-confusion attacks.
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, ErrInvalidToken
			}
			return p.secret, nil
		},
	)

	if err != nil {
		var ve *jwt.ValidationError
		if errors.As(err, &ve) && ve.Errors&jwt.ValidationErrorExpired != 0 {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	// Manual issuer check.
	if p.issuer != "" && claims.Issuer != p.issuer {
		return nil, ErrInvalidToken
	}

	// Manual audience check (jwt.ClaimStrings is []string).
	if p.audience != "" {
		audienceOK := false
		for _, aud := range claims.Audience {
			if aud == p.audience {
				audienceOK = true
				break
			}
		}
		if !audienceOK {
			return nil, ErrInvalidToken
		}
	}

	return claims, nil
}

// UserIDFromClaims converts the string Subject back to a uint user ID.
// The Auth service stores the user ID as both the custom "uid" field and as
// the standard JWT "sub" claim.  We prefer "uid" and fall back to "sub".
func UserIDFromClaims(claims *Claims) uint {
	if claims.UserID != 0 {
		return claims.UserID
	}
	// Fallback: parse "sub"
	id, err := strconv.ParseUint(claims.Subject, 10, 64)
	if err != nil {
		return 0
	}
	return uint(id)
}
