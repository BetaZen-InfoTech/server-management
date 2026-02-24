package services

import (
	"context"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/mongo"
)

type AuthService struct {
	db *mongo.Database
}

func NewAuthService(db *mongo.Database) *AuthService {
	return &AuthService{db: db}
}

// Login authenticates a user with email, password, and optional TOTP code.
func (s *AuthService) Login(ctx context.Context, req *models.LoginRequest, ip string) (*models.LoginResponse, error) {
	// TODO: implement - validate credentials, check 2FA, generate tokens
	return nil, nil
}

// RefreshToken issues a new access token using a valid refresh token.
func (s *AuthService) RefreshToken(ctx context.Context, refreshToken string) (*models.LoginResponse, error) {
	// TODO: implement - validate refresh token, issue new access token
	return nil, nil
}

// Logout invalidates the given refresh token.
func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	// TODO: implement - clear refresh token from user record
	return nil
}

// ForgotPassword sends a password reset email to the given address.
func (s *AuthService) ForgotPassword(ctx context.Context, email string) error {
	// TODO: implement - generate reset token, send email
	return nil
}

// ResetPassword changes the user's password using a valid reset token.
func (s *AuthService) ResetPassword(ctx context.Context, token string, newPassword string) error {
	// TODO: implement - validate reset token, hash and save new password
	return nil
}

// Enable2FA generates a TOTP secret and returns setup data (QR code, secret, recovery codes).
func (s *AuthService) Enable2FA(ctx context.Context, userID string) (map[string]interface{}, error) {
	// TODO: implement - generate TOTP secret, return QR URI and recovery codes
	return nil, nil
}

// Verify2FA confirms the TOTP code and activates 2FA for the user.
func (s *AuthService) Verify2FA(ctx context.Context, userID string, code string) error {
	// TODO: implement - verify TOTP code against stored secret, enable 2FA
	return nil
}

// Disable2FA turns off two-factor authentication for the user.
func (s *AuthService) Disable2FA(ctx context.Context, userID string) error {
	// TODO: implement - clear 2FA secret, disable flag
	return nil
}
