package service

import (
	"errors"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrWeakPassword = errors.New(
		"password must be 8-72 characters and contain uppercase, lowercase, digit and special character",
	)
)

// HashPassword returns the bcrypt hash of the plaintext password.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword reports whether the plaintext password matches the stored hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// ValidatePassword enforces the password policy:
//   - 8-72 characters
//   - at least one uppercase letter
//   - at least one lowercase letter
//   - at least one digit
//   - at least one punctuation / symbol character
func ValidatePassword(password string) error {
	if len(password) < 8 || len(password) > 72 {
		return ErrWeakPassword
	}

	var hasUpper, hasLower, hasDigit, hasSpecial bool

	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSpecial = true
		}
	}

	if !hasUpper || !hasLower || !hasDigit || !hasSpecial {
		return ErrWeakPassword
	}

	return nil
}
