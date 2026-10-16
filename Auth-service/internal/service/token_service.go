package service

import (
	"Auth-service/internal/models"
	"Auth-service/internal/repository"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

var (
	ErrInvalidToken  = errors.New("invalid or expired token")
	ErrExpiredToken  = errors.New("token has expired")
)

// Claims is the JWT payload used by this service.
type Claims struct {
	UserID uint   `json:"uid"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// TokenService handles JWT access tokens and opaque refresh tokens.
type TokenService struct {
	repo                *repository.RefreshTokenRepository
	jwtSecret           string
	accessTokenExpiry   time.Duration
	refreshTokenExpiry  time.Duration
	issuer              string
}

func NewTokenService(
	repo *repository.RefreshTokenRepository,
	jwtSecret string,
	accessTokenExpiry time.Duration,
	refreshTokenExpiry time.Duration,
	issuer string,
) *TokenService {
	return &TokenService{
		repo:               repo,
		jwtSecret:          jwtSecret,
		accessTokenExpiry:  accessTokenExpiry,
		refreshTokenExpiry: refreshTokenExpiry,
		issuer:             issuer,
	}
}

// GenerateAccessToken signs a short-lived JWT for the given user.
func (s *TokenService) GenerateAccessToken(user *models.User) (string, error) {
	now := time.Now()

	claims := Claims{
		UserID: user.ID,
		Role:   user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatUint(uint64(user.ID), 10),
			Issuer:    s.issuer,
			Audience:  jwt.ClaimStrings{"commerce-api"},
			ExpiresAt: jwt.NewNumericDate(now.Add(s.accessTokenExpiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.jwtSecret))
}

// ValidateAccessToken parses and validates a JWT, returning its claims.
// jwt.WithAudience / jwt.WithIssuer / jwt.WithValidMethods are v5-only;
// in v4 we validate those fields manually after parsing.
func (s *TokenService) ValidateAccessToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		func(t *jwt.Token) (interface{}, error) {
			// Reject any non-HMAC algorithm to prevent algorithm-confusion attacks.
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, ErrInvalidToken
			}
			return []byte(s.jwtSecret), nil
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
	if claims.Issuer != s.issuer {
		return nil, ErrInvalidToken
	}

	// Manual audience check (jwt.ClaimStrings is []string).
	audienceOK := false
	for _, aud := range claims.Audience {
		if aud == "commerce-api" {
			audienceOK = true
			break
		}
	}
	if !audienceOK {
		return nil, ErrInvalidToken
	}

	return claims, nil
}

// GenerateRefreshToken creates a cryptographically random opaque token,
// persists its SHA-256 hash, and returns the plaintext to be sent to the client.
func (s *TokenService) GenerateRefreshToken(userID uint) (string, error) {
	// 32 random bytes → 64-char hex string
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}

	plaintext := hex.EncodeToString(raw)
	hash := hashToken(plaintext)

	record := &models.RefreshToken{
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: time.Now().Add(s.refreshTokenExpiry),
	}

	if err := s.repo.Create(record); err != nil {
		return "", err
	}

	return plaintext, nil
}

// RotateRefreshToken validates the old token, deletes it, and issues fresh
// access + refresh tokens (token rotation prevents replay attacks).
func (s *TokenService) RotateRefreshToken(
	plaintext string,
	userRepo *repository.UserRepository,
) (string, string, *models.User, error) {
	hash := hashToken(plaintext)

	record, err := s.repo.FindByTokenHash(hash)
	if err != nil {
		return "", "", nil, err
	}
	if record == nil {
		return "", "", nil, ErrInvalidToken
	}

	// Delete the consumed token immediately (rotation).
	if err := s.repo.DeleteByTokenHash(hash); err != nil {
		return "", "", nil, err
	}

	user, err := userRepo.FindByID(record.UserID)
	if err != nil || user == nil {
		return "", "", nil, ErrInvalidToken
	}

	accessToken, err := s.GenerateAccessToken(user)
	if err != nil {
		return "", "", nil, err
	}

	newRefresh, err := s.GenerateRefreshToken(user.ID)
	if err != nil {
		return "", "", nil, err
	}

	return accessToken, newRefresh, user, nil
}

// RevokeRefreshToken deletes a single refresh token (single-device logout).
func (s *TokenService) RevokeRefreshToken(plaintext string) error {
	return s.repo.DeleteByTokenHash(hashToken(plaintext))
}

// RevokeAllRefreshTokens deletes every refresh token for a user (global logout).
func (s *TokenService) RevokeAllRefreshTokens(userID uint) error {
	return s.repo.DeleteAllForUser(userID)
}

// hashToken returns the hex-encoded SHA-256 of the plaintext token.
func hashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
