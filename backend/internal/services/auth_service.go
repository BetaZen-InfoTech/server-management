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
	"go.mongodb.org/mongo-driver/bson/primitive"
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
		if user.FailedLogins+1 >= 20 {
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

	// Determine refresh token expiry: 30 days if remember_me, otherwise 7 days
	refreshExpiry := s.cfg.JWTRefreshExpiry // default 7 days
	if req.RememberMe {
		refreshExpiry = 30 * 24 * time.Hour
	}

	// Generate access token — use Full so is_super_admin travels in the
	// JWT. The impersonated_by field is empty here; it's only set by the
	// /admin/vendors/:id/impersonate flow.
	accessToken, err := jwt.GenerateAccessTokenFull(
		s.cfg.JWTSecret,
		s.cfg.JWTAccessExpiry,
		user.ID.Hex(),
		user.Email,
		user.Role,
		resolveTenantID(&user),
		perms,
		user.IsSuperAdmin,
		"",
	)
	if err != nil {
		return nil, errors.New("failed to generate token")
	}

	// Generate refresh token
	refreshToken, err := jwt.GenerateRefreshToken()
	if err != nil {
		return nil, errors.New("failed to generate refresh token")
	}

	// Save refresh token, IP, expiry, and reset failed logins
	now := time.Now()
	refreshExpiresAt := now.Add(refreshExpiry)
	_, _ = col.UpdateByID(ctx, user.ID, bson.M{
		"$set": bson.M{
			"refresh_token":      refreshToken,
			"refresh_expires_at": refreshExpiresAt,
			"failed_logins":      0,
			"locked_until":       nil,
			"last_login":         now,
			"last_login_ip":      ip,
			"updated_at":         now,
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

	// Check if refresh token has expired
	if user.RefreshExpiresAt != nil && user.RefreshExpiresAt.Before(time.Now()) {
		// Clear expired token
		_, _ = col.UpdateByID(ctx, user.ID, bson.M{
			"$set": bson.M{"refresh_token": "", "refresh_expires_at": nil, "updated_at": time.Now()},
		})
		return nil, errors.New("refresh token expired, please login again")
	}

	// Fall back to default permissions if user has none stored
	perms := user.Permissions
	if len(perms) == 0 {
		perms = constants.DefaultPermissions[user.Role]
	}

	// Generate new access token — include is_super_admin so the refreshed
	// token keeps the same capabilities as the original login.
	accessToken, err := jwt.GenerateAccessTokenFull(
		s.cfg.JWTSecret,
		s.cfg.JWTAccessExpiry,
		user.ID.Hex(),
		user.Email,
		user.Role,
		resolveTenantID(&user),
		perms,
		user.IsSuperAdmin,
		"",
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

// Impersonate issues a short-lived access token for `targetUserID` on behalf
// of `adminUserID`. Used by the "Login as vendor" button on the WHM Vendors
// page so an admin can reach the vendor's panel without asking for their
// password. The returned JWT carries `impersonated_by = adminUserID` so the
// frontend can show a banner and every audit log entry can identify who's
// behind the wheel. No refresh token is issued — re-impersonation is a
// fresh, auditable action.
func (s *AuthService) Impersonate(ctx context.Context, adminUserID, targetUserID string) (*models.LoginResponse, error) {
	targetOID, err := primitive.ObjectIDFromHex(targetUserID)
	if err != nil {
		return nil, errors.New("invalid target user id")
	}
	col := s.db.Collection(database.ColUsers)
	var target models.User
	if err := col.FindOne(ctx, bson.M{"_id": targetOID}).Decode(&target); err != nil {
		return nil, errors.New("target user not found")
	}
	if target.DeletedAt != nil {
		return nil, errors.New("cannot impersonate a trashed user")
	}
	if !target.IsActive {
		return nil, errors.New("cannot impersonate a suspended user")
	}

	perms := target.Permissions
	if len(perms) == 0 {
		perms = constants.DefaultPermissions[target.Role]
	}

	// Impersonation tokens are short-lived — 15 minutes regardless of the
	// server's JWTAccessExpiry — so an unattended admin session doesn't
	// stay in someone else's account indefinitely.
	expiry := 15 * time.Minute
	token, err := jwt.GenerateAccessTokenFull(
		s.cfg.JWTSecret,
		expiry,
		target.ID.Hex(),
		target.Email,
		target.Role,
		resolveTenantID(&target),
		perms,
		target.IsSuperAdmin,
		adminUserID,
	)
	if err != nil {
		return nil, errors.New("failed to generate impersonation token")
	}
	return &models.LoginResponse{
		AccessToken: token,
		// No refresh token — impersonation must be explicitly re-issued.
		RefreshToken: "",
		ExpiresIn:    int(expiry.Seconds()),
		TokenType:    "Bearer",
		User:         &target,
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
