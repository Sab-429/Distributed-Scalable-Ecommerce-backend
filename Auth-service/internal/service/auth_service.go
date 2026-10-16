package service

import (
	"Auth-service/internal/dto"
	"Auth-service/internal/models"
	"Auth-service/internal/repository"
	"errors"
	"strings"
	"unicode"
)

var (
	ErrInvalidName        = errors.New("name must be 2-100 non-control characters")
	ErrInvalidEmail       = errors.New("invalid email address")
	ErrEmailAlreadyExists = errors.New("email already registered")
	ErrInvalidCredentials = errors.New("invalid email or password")
)

// AuthService orchestrates registration, login, token refresh and logout.
type AuthService struct {
	userRepo  *repository.UserRepository
	tokenSvc  *TokenService
}

func NewAuthService(
	userRepo *repository.UserRepository,
	tokenSvc *TokenService,
) *AuthService {
	return &AuthService{
		userRepo: userRepo,
		tokenSvc: tokenSvc,
	}
}

// Register validates input, creates a new user and returns a RegisterResponse.
func (s *AuthService) Register(req dto.RegisterRequest) (*dto.RegisterResponse, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Email = normalizeEmail(req.Email)

	if err := validateName(req.Name); err != nil {
		return nil, err
	}

	if err := validateEmail(req.Email); err != nil {
		return nil, err
	}

	if err := ValidatePassword(req.Password); err != nil {
		return nil, err
	}

	// Duplicate email check.
	existing, err := s.userRepo.FindByEmail(req.Email)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrEmailAlreadyExists
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	user := &models.User{
		Name:         req.Name,
		Email:        req.Email,
		PasswordHash: hash,
		Role:         "user",
	}

	if err := s.userRepo.Create(user); err != nil {
		return nil, err
	}

	return &dto.RegisterResponse{
		Message: "account created successfully",
		User:    toUserResponse(user),
	}, nil
}

// Login verifies credentials and issues an access + refresh token pair.
func (s *AuthService) Login(req dto.LoginRequest) (*dto.AuthResponse, error) {
	req.Email = normalizeEmail(req.Email)

	if req.Email == "" || req.Password == "" {
		return nil, ErrInvalidCredentials
	}

	user, err := s.userRepo.FindByEmail(req.Email)
	if err != nil {
		return nil, err
	}
	if user == nil || !CheckPassword(user.PasswordHash, req.Password) {
		return nil, ErrInvalidCredentials
	}

	return s.issueTokenPair(user)
}

// RefreshTokens validates a refresh token and rotates both tokens.
func (s *AuthService) RefreshTokens(req dto.RefreshRequest) (*dto.AuthResponse, error) {
	accessToken, refreshToken, user, err := s.tokenSvc.RotateRefreshToken(
		req.RefreshToken,
		s.userRepo,
	)
	if err != nil {
		return nil, err
	}

	return buildAuthResponse(accessToken, refreshToken, user), nil
}

// Logout revokes a single refresh token (device-level logout).
func (s *AuthService) Logout(req dto.LogoutRequest) error {
	return s.tokenSvc.RevokeRefreshToken(req.RefreshToken)
}

// LogoutAll revokes every refresh token for a user (global logout).
func (s *AuthService) LogoutAll(userID uint) error {
	return s.tokenSvc.RevokeAllRefreshTokens(userID)
}

// Me returns the public profile for the authenticated user.
func (s *AuthService) Me(userID uint) (*dto.UserResponse, error) {
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrInvalidCredentials
	}
	return toUserResponse(user), nil
}

// issueTokenPair generates an access + refresh token pair for the given user.
func (s *AuthService) issueTokenPair(user *models.User) (*dto.AuthResponse, error) {
	accessToken, err := s.tokenSvc.GenerateAccessToken(user)
	if err != nil {
		return nil, err
	}

	refreshToken, err := s.tokenSvc.GenerateRefreshToken(user.ID)
	if err != nil {
		return nil, err
	}

	return buildAuthResponse(accessToken, refreshToken, user), nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func buildAuthResponse(
	accessToken, refreshToken string,
	user *models.User,
) *dto.AuthResponse {
	return &dto.AuthResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    int64(86400), // 24 h in seconds (matches default config)
		User:         toUserResponse(user),
	}
}

func toUserResponse(u *models.User) *dto.UserResponse {
	return &dto.UserResponse{
		ID:        u.ID,
		Name:      u.Name,
		Email:     u.Email,
		Role:      u.Role,
		CreatedAt: u.CreatedAt,
	}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func validateName(name string) error {
	if len(name) < 2 || len(name) > 100 {
		return ErrInvalidName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return ErrInvalidName
		}
	}
	return nil
}

func validateEmail(email string) error {
	if email == "" || len(email) > 254 {
		return ErrInvalidEmail
	}
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return ErrInvalidEmail
	}
	return nil
}
