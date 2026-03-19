package services

import (
	"context"
	"errors"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/jwt"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/crypto/bcrypt"
)

type AuthService struct {
	db  *mongo.Database
	cfg *config.Config
}

func NewAuthService(db *mongo.Database, cfg *config.Config) *AuthService {
	return &AuthService{db: db, cfg: cfg}
}

func (s *AuthService) Login(ctx context.Context, req *models.LoginRequest, ip string) (*models.LoginResponse, error) {
	col := s.db.Collection(database.ColUsers)

	// Find user by email
	var user models.User
	err := col.FindOne(ctx, bson.M{"email": req.Email}).Decode(&user)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.New("invalid email or password")
		}
		return nil, errors.New("login failed")
	}

	// Check if account is active
	if !user.IsActive {
		return nil, errors.New("account is disabled")
	}

	// Check if account is locked
	if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		return nil, errors.New("account is temporarily locked, try again later")
	}

	// Compare password
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		// Increment failed login counter
		update := bson.M{"$inc": bson.M{"failed_logins": 1}}
		if user.FailedLogins+1 >= 5 {
			lockUntil := time.Now().Add(15 * time.Minute)
			update["$set"] = bson.M{"locked_until": lockUntil}
		}
		_, _ = col.UpdateByID(ctx, user.ID, update)
		return nil, errors.New("invalid email or password")
	}

	// Fall back to default permissions if user has none stored
	perms := user.Permissions
	if len(perms) == 0 {
		perms = constants.DefaultPermissions[user.Role]
	}

	// Generate access token
	accessToken, err := jwt.GenerateAccessToken(
		s.cfg.JWTSecret,
		s.cfg.JWTAccessExpiry,
		user.ID.Hex(),
		user.Email,
		user.Role,
		perms,
	)
	if err != nil {
		return nil, errors.New("failed to generate token")
	}

	// Generate refresh token
	refreshToken, err := jwt.GenerateRefreshToken()
	if err != nil {
		return nil, errors.New("failed to generate refresh token")
	}

	// Save refresh token and reset failed logins
	now := time.Now()
	_, _ = col.UpdateByID(ctx, user.ID, bson.M{
		"$set": bson.M{
			"refresh_token": refreshToken,
			"failed_logins": 0,
			"locked_until":  nil,
			"last_login":    now,
			"updated_at":    now,
		},
	})

	return &models.LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int(s.cfg.JWTAccessExpiry.Seconds()),
		TokenType:    "Bearer",
		User:         &user,
	}, nil
}

func (s *AuthService) RefreshToken(ctx context.Context, refreshToken string) (*models.LoginResponse, error) {
	col := s.db.Collection(database.ColUsers)

	// Find user by refresh token
	var user models.User
	err := col.FindOne(ctx, bson.M{"refresh_token": refreshToken}).Decode(&user)
	if err != nil {
		return nil, errors.New("invalid refresh token")
	}

	if !user.IsActive {
		return nil, errors.New("account is disabled")
	}

	// Fall back to default permissions if user has none stored
	perms := user.Permissions
	if len(perms) == 0 {
		perms = constants.DefaultPermissions[user.Role]
	}

	// Generate new access token
	accessToken, err := jwt.GenerateAccessToken(
		s.cfg.JWTSecret,
		s.cfg.JWTAccessExpiry,
		user.ID.Hex(),
		user.Email,
		user.Role,
		perms,
	)
	if err != nil {
		return nil, errors.New("failed to generate token")
	}

	// Generate new refresh token
	newRefreshToken, err := jwt.GenerateRefreshToken()
	if err != nil {
		return nil, errors.New("failed to generate refresh token")
	}

	// Rotate refresh token
	_, _ = col.UpdateByID(ctx, user.ID, bson.M{
		"$set": bson.M{
			"refresh_token": newRefreshToken,
			"updated_at":    time.Now(),
		},
	})

	return &models.LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		ExpiresIn:    int(s.cfg.JWTAccessExpiry.Seconds()),
		TokenType:    "Bearer",
		User:         &user,
	}, nil
}

func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	col := s.db.Collection(database.ColUsers)
	_, err := col.UpdateOne(ctx, bson.M{"refresh_token": refreshToken}, bson.M{
		"$set": bson.M{"refresh_token": "", "updated_at": time.Now()},
	})
	return err
}

func (s *AuthService) ForgotPassword(ctx context.Context, email string) error {
	// TODO: implement - generate reset token, send email
	return nil
}

func (s *AuthService) ResetPassword(ctx context.Context, token string, newPassword string) error {
	// TODO: implement - validate reset token, hash and save new password
	return nil
}

func (s *AuthService) Enable2FA(ctx context.Context, userID string) (map[string]interface{}, error) {
	// TODO: implement - generate TOTP secret, return QR URI and recovery codes
	return nil, nil
}

func (s *AuthService) Verify2FA(ctx context.Context, userID string, code string) error {
	// TODO: implement - verify TOTP code against stored secret, enable 2FA
	return nil
}

func (s *AuthService) Disable2FA(ctx context.Context, userID string) error {
	// TODO: implement - clear 2FA secret, disable flag
	return nil
}
