package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/geoip"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/jwt"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/mailer"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/uaparse"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

type AuthService struct {
	db         *mongo.Database
	cfg        *config.Config
	m          *mailer.Mailer // shared handle — can be swapped via SetMailer when PanelMailService reloads
	resetURLFn func(token, surface string) string
}

// surfaceWHM / surfaceUserPanel are the two URL surfaces the reset
// link can land on. "whm" goes to /whm/reset-password (the platform
// owner's panel). Anything else (or empty) goes to
// /user-panel/reset-password since that's where every non-owner role
// signs in. Centralising the literal strings here means the handler
// only has to pass through whatever the caller sent — no per-call
// string fiddling.
const (
	surfaceWHM       = "whm"
	surfaceUserPanel = "user-panel"
)

// normaliseSurface accepts the small set of surface hints the
// frontend sends (`whm`, `user-panel`, plus historical aliases like
// `cpanel` from the pre-rename release that 301-redirects to
// /user-panel) and maps them to the canonical constants. Unknown
// values return "" so the caller can fall back to role-based
// defaults instead of building a broken URL.
func normaliseSurface(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case surfaceWHM:
		return surfaceWHM
	case surfaceUserPanel, "cpanel", "user_panel", "userpanel":
		return surfaceUserPanel
	}
	return ""
}

func NewAuthService(db *mongo.Database, cfg *config.Config) *AuthService {
	// Default reset URL builder picks the surface based on the hint
	// from the originating page (whm forgot-password page sends
	// surface=whm; user-panel forgot-password page sends
	// surface=user-panel). When the hint is empty we default to
	// /user-panel since that's the larger audience and a vendor
	// landing on the wrong panel is a more confusing failure than
	// the platform owner doing so.
	return &AuthService{
		db:  db,
		cfg: cfg,
		resetURLFn: func(token, surface string) string {
			path := "/user-panel/reset-password"
			if surface == surfaceWHM {
				path = "/whm/reset-password"
			}
			return fmt.Sprintf("https://%s%s?token=%s", cfg.Domain, path, token)
		},
	}
}

// SetMailer wires the shared mailer so ForgotPassword can send the
// reset link. Called once from main.go after PanelMailService boots.
func (s *AuthService) SetMailer(m *mailer.Mailer) { s.m = m }

// SetResetURLBuilder lets tests / alternate deployments override the
// default surface-aware "https://<Domain>/{whm,user-panel}/reset-password?token=X"
// shape. The override receives both the token and the surface hint
// the caller sent so custom builders can keep the role-routing
// behaviour.
func (s *AuthService) SetResetURLBuilder(fn func(token, surface string) string) {
	if fn != nil {
		s.resetURLFn = fn
	}
}

func (s *AuthService) Login(ctx context.Context, req *models.LoginRequest, ip string) (*models.LoginResponse, error) {
	return s.LoginWithUA(ctx, req, ip, "")
}

// LoginWithUA is the real entry point — old Login() forwards for
// back-compat. We split so the handler can thread the User-Agent
// header through for RecordLoginSession without changing every caller
// that only has ip.
func (s *AuthService) LoginWithUA(ctx context.Context, req *models.LoginRequest, ip, userAgent string) (*models.LoginResponse, error) {
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

	// Audit: record the login session (geoip + UA parse happen async so
	// the response isn't blocked on ip-api.com). Only on successful
	// password logins — failed attempts stay in audit_logs.
	go s.RecordLoginSession(context.Background(), &user, "password", ip, userAgent)

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

// forgotPasswordMinGap rate-limits how often the same user can request
// a new reset token. Short enough that an honest operator who mistypes
// their email can retry quickly, long enough that an abuser can't
// pump the relay with requests for a known address.
const forgotPasswordMinGap = 60 * time.Second

// resetTokenTTL caps the useful life of a password-reset link. A
// stolen / leaked link becomes useless after this window.
const resetTokenTTL = 30 * time.Minute

// ForgotPassword generates a reset token for the given email, stores
// its SHA-256 hash + expiry on the user record, and emails the link
// to the user's primary (or recovery) email.
//
// Security properties:
//   - The raw token is NEVER stored — only SHA-256(token). A DB dump
//     alone cannot be used to reset anyone's password.
//   - Returns nil even when the email doesn't exist, so an attacker
//     can't enumerate registered accounts via the response.
//   - 60-second rate limit per user.
//   - 30-minute token lifetime.
//   - If SMTP isn't configured we still persist the token and log the
//     URL to stderr so the operator can recover in a fresh install
//     before they've set up mail.
//
// `surface` is the originating panel ("whm" or "user-panel"). The
// builder uses it to send vendor_owner-side requests to
// /whm/reset-password and everyone else to /user-panel/reset-password.
// For platform-owner accounts that requested from the User Panel by
// mistake we still respect the surface override so the operator gets
// where they expect — the panel-side login pages enforce role
// matching anyway.
func (s *AuthService) ForgotPassword(ctx context.Context, email, surface string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil // silently succeed — never leak which emails are registered
	}
	col := s.db.Collection(database.ColUsers)

	// Look up by primary email OR recovery email so operators can use
	// either address when their primary mail domain is down.
	var user models.User
	err := col.FindOne(ctx, bson.M{
		"$or": bson.A{
			bson.M{"email": email},
			bson.M{"recovery_email": email},
		},
		"deleted_at": nil,
		"is_active":  true,
	}).Decode(&user)
	if err == mongo.ErrNoDocuments {
		// Don't even log here — log noise on enumeration probes is
		// itself a side-channel.
		return nil
	}
	if err != nil {
		log.Warn().Err(err).Str("email", email).Msg("forgot-password: user lookup failed")
		return err
	}

	// Rate-limit: block a new request within 60s of the previous one.
	if user.ResetRequestedAt != nil && time.Since(*user.ResetRequestedAt) < forgotPasswordMinGap {
		log.Info().Str("user_id", user.ID.Hex()).Str("email", user.Email).Msg("forgot-password: rate-limited")
		return nil // silently succeed from the caller's POV
	}

	// Generate a 32-byte token; hex-encode for URL safety.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])

	expires := time.Now().Add(resetTokenTTL)
	now := time.Now()
	if _, err := col.UpdateOne(ctx, bson.M{"_id": user.ID}, bson.M{"$set": bson.M{
		"reset_token_hash":   hash,
		"reset_expires_at":   expires,
		"reset_requested_at": now,
		"updated_at":         now,
	}}); err != nil {
		return err
	}

	// Send to whichever address the user typed. The OR-find above
	// already accepts either primary OR recovery — `email` IS the
	// matching one, so honouring it sends the link where the user
	// expects (their primary mail domain may be the broken one).
	sendTo := email

	// Pick the surface: explicit override from the calling page,
	// otherwise default by role (vendor_owner → whm, everyone else
	// → user-panel). This matches the strict login split — owner
	// accounts can't sign in at /user-panel/login and vice versa.
	resolvedSurface := normaliseSurface(surface)
	if resolvedSurface == "" {
		if user.Role == "vendor_owner" {
			resolvedSurface = surfaceWHM
		} else {
			resolvedSurface = surfaceUserPanel
		}
	}
	resetURL := s.resetURLFn(token, resolvedSurface)

	// If mailer isn't wired or isn't configured, log the URL to stderr
	// as a fresh-install escape hatch and still report success. The
	// operator can copy/paste it from the journalctl tail. Also log a
	// structured warn — the SMTP misconfiguration is the actual root
	// cause every "forgot password sends nothing" report ends up at.
	if s.m == nil || !s.m.Enabled() {
		log.Warn().
			Str("user_id", user.ID.Hex()).
			Str("email", sendTo).
			Msg("forgot-password: SMTP not configured — reset link only available via stderr")
		fmt.Printf("[auth] password reset link (mailer disabled) for %s: %s\n", sendTo, resetURL)
		return nil
	}

	subject, text, html, err := mailer.BuildPasswordReset(mailer.PasswordResetData{
		Name:        user.Name,
		Email:       user.Email,
		Role:        user.Role,
		ResetURL:    resetURL,
		ExpiresMin:  int(resetTokenTTL / time.Minute),
		PanelName:   "Betazen Server Panel",
		SupportFrom: s.m.Config().FromAddr,
	})
	if err != nil {
		return err
	}
	// Send in the foreground so a SMTP failure surfaces to the handler.
	// Public forgot-password rightly ignores this error (enumeration
	// resistance), but we always log the structured reason so an
	// operator tailing journalctl can see exactly why a delivery
	// failed — auth was rejected, port blocked, relay down, etc.
	if err := s.m.Send(ctx, mailer.Message{
		To:      sendTo,
		Subject: subject,
		Text:    text,
		HTML:    html,
	}); err != nil {
		log.Error().
			Err(err).
			Str("user_id", user.ID.Hex()).
			Str("to", sendTo).
			Str("smtp_host", s.m.Config().Host).
			Int("smtp_port", s.m.Config().Port).
			Msg("forgot-password: SMTP send failed — reset link not delivered")
		return err
	}
	log.Info().
		Str("user_id", user.ID.Hex()).
		Str("to", sendTo).
		Str("surface", resolvedSurface).
		Msg("forgot-password: reset link sent")
	return nil
}

// ResetPassword validates a reset token, bcrypt-hashes the new password,
// and stores it. Also clears the token fields and bumps RefreshToken so
// any existing sessions (potentially the attacker's) are invalidated.
func (s *AuthService) ResetPassword(ctx context.Context, token string, newPassword string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("reset token is required")
	}
	if len(newPassword) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])

	col := s.db.Collection(database.ColUsers)
	var user models.User
	err := col.FindOne(ctx, bson.M{
		"reset_token_hash": hash,
		"reset_expires_at": bson.M{"$gt": time.Now()},
		"deleted_at":       nil,
	}).Decode(&user)
	if err == mongo.ErrNoDocuments {
		return errors.New("this reset link is invalid or has expired — request a new one")
	}
	if err != nil {
		return err
	}

	pwHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	now := time.Now()
	_, err = col.UpdateOne(ctx, bson.M{"_id": user.ID}, bson.M{
		"$set": bson.M{
			"password":   string(pwHash),
			"updated_at": now,
		},
		"$unset": bson.M{
			"reset_token_hash":   "",
			"reset_expires_at":   "",
			"reset_requested_at": "",
			"refresh_token":      "", // invalidate existing sessions
			"refresh_expires_at": "",
		},
	})
	return err
}

// Me returns the currently-authenticated user's profile — the bits the
// Profile page needs to render the form. No sensitive fields leak
// because models.User already hides password/refresh token via `json:"-"`.
func (s *AuthService) Me(ctx context.Context, userID string) (*models.User, error) {
	objID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return nil, errors.New("invalid user id")
	}
	var u models.User
	if err := s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": objID}).Decode(&u); err != nil {
		return nil, errors.New("user not found")
	}
	return &u, nil
}

// UpdateProfile lets the signed-in user edit their own name / email. Email
// changes are case-normalised and must stay globally unique (matches the
// Create flow). Role and permissions are intentionally NOT editable here —
// that belongs to an admin via the Users page.
func (s *AuthService) UpdateProfile(ctx context.Context, userID string, name, email string) (*models.User, error) {
	objID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return nil, errors.New("invalid user id")
	}
	col := s.db.Collection(database.ColUsers)
	set := bson.M{"updated_at": time.Now()}
	name = strings.TrimSpace(name)
	email = strings.ToLower(strings.TrimSpace(email))
	if name != "" {
		set["name"] = name
	}
	if email != "" {
		// Uniqueness check — exclude the user's own row so re-saving the
		// same email is a no-op instead of a conflict.
		var existing models.User
		err := col.FindOne(ctx, bson.M{
			"email": email,
			"_id":   bson.M{"$ne": objID},
		}).Decode(&existing)
		if err == nil {
			return nil, errors.New("that email is already in use")
		}
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return nil, err
		}
		set["email"] = email
	}
	if _, err := col.UpdateByID(ctx, objID, bson.M{"$set": set}); err != nil {
		return nil, err
	}
	var updated models.User
	if err := col.FindOne(ctx, bson.M{"_id": objID}).Decode(&updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

// ChangePassword lets a signed-in user rotate their own password. Requires
// the current password so a stolen access token alone can't lock the
// account. On success the refresh token is cleared so every OTHER active
// session is invalidated — the current session keeps working because the
// frontend holds an access token issued before the change.
func (s *AuthService) ChangePassword(ctx context.Context, userID, current, next string) error {
	if len(next) < 8 {
		return errors.New("new password must be at least 8 characters")
	}
	objID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return errors.New("invalid user id")
	}
	col := s.db.Collection(database.ColUsers)
	var u models.User
	if err := col.FindOne(ctx, bson.M{"_id": objID}).Decode(&u); err != nil {
		return errors.New("user not found")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(current)); err != nil {
		return errors.New("current password is incorrect")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err := col.UpdateByID(ctx, objID, bson.M{
		"$set": bson.M{
			"password":   string(hash),
			"updated_at": time.Now(),
		},
		"$unset": bson.M{
			"refresh_token":      "",
			"refresh_expires_at": "",
		},
	}); err != nil {
		return err
	}
	// Mirror to the Linux account so SSH / FTP stay in sync — matches
	// what the admin-driven ResetPassword flow does.
	if u.Username != "" {
		agent.SetLinuxUserPassword(ctx, u.Username, next)
	}
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

// ─── OTP email login ────────────────────────────────────────────────
//
// Passwordless login flow: caller POSTs an email → we generate a random
// 10-char alphanumeric code, store its SHA-256 hash, and email the raw
// code + a magic URL that prefills the OTP page. The code expires in
// 10 minutes and can be consumed at most once.
//
// Security / abuse properties:
//   - Raw code is NEVER stored. DB dump cannot replay logins.
//   - Per-user rate limit (60s between requests) so an abuser can't
//     spam someone's inbox.
//   - Success response is identical whether the email exists or not —
//     no account enumeration.
//   - Verification attempts are capped at 5 per OTP to stop a dictionary
//     attack against a leaked (but unused) email.
//   - 5-minute log-of-failed-verifications rate limit on the handler
//     side (`LoginRateLimiter()` middleware) protects the endpoint.

const (
	otpTTL            = 10 * time.Minute
	otpMinGap         = 60 * time.Second
	otpMaxAttempts    = 5
	otpCodeLength     = 10 // alphanumeric, 10 chars of A-Za-z0-9
	otpAlphabet       = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
)

// generateOTPCode returns a cryptographically-random alphanumeric code
// using a reduced alphabet that omits confusable characters (0/O, 1/l/I).
// Picking the alphabet through rand.Int preserves uniform distribution.
func generateOTPCode() (string, error) {
	b := make([]byte, otpCodeLength)
	n := byte(len(otpAlphabet))
	// Read enough random bytes, then map each byte into the alphabet
	// range via rejection sampling so the distribution stays uniform.
	buf := make([]byte, otpCodeLength*2)
	filled := 0
	for filled < otpCodeLength {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for i := 0; i < len(buf) && filled < otpCodeLength; i++ {
			// Rejection sampling: skip bytes that'd introduce bias.
			limit := byte(256 - (256 % int(n)))
			if buf[i] >= limit {
				continue
			}
			b[filled] = otpAlphabet[buf[i]%n]
			filled++
		}
	}
	return string(b), nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// otpMagicURL builds the public click-link that lands on the OTP page
// with email + code prefilled. Surface routing matches the
// reset-password URL builder.
func (s *AuthService) otpMagicURL(email, code, surface string) string {
	path := "/user-panel/otp"
	if surface == surfaceWHM {
		path = "/whm/otp"
	}
	// email and code are URL-safe by construction (email is already
	// trimmed/lowercased; code uses a constrained alphabet). Still use
	// query encoding to be defensive against future changes.
	q := url.Values{}
	q.Set("email", email)
	q.Set("code", code)
	return fmt.Sprintf("https://%s%s?%s", s.cfg.Domain, path, q.Encode())
}

// RequestOTP generates and emails a one-time login code for `email`.
// Intentionally returns nil on user-not-found to prevent enumeration —
// SMTP failures are the only non-nil error path, and even those are
// swallowed by the public handler.
//
// surface controls which SPA the magic link points at ("whm" or
// "user-panel"). When empty we infer from role (vendor_owner → whm).
func (s *AuthService) RequestOTP(ctx context.Context, email, surface, ip, userAgent string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil
	}
	users := s.db.Collection(database.ColUsers)

	// Find the target user. OTP login is only issued to real, active,
	// non-trashed accounts. Returning nil-without-email-sent on the
	// miss preserves the enumeration-resistance contract.
	var user models.User
	err := users.FindOne(ctx, bson.M{
		"email":      email,
		"deleted_at": nil,
		"is_active":  true,
	}).Decode(&user)
	if err == mongo.ErrNoDocuments {
		log.Info().Str("email", email).Msg("otp-request: no such user — swallowing to avoid enumeration")
		return nil
	}
	if err != nil {
		return err
	}

	// Per-user rate limit — block a second request within 60s of the
	// previous one. Using the `created_at` of the most recent non-used
	// OTP as the gate.
	otps := s.db.Collection(database.ColOTPRequests)
	var last models.OTPRequest
	err = otps.FindOne(ctx, bson.M{
		"email": email,
		"used":  false,
	}, options.FindOne().SetSort(bson.D{{Key: "created_at", Value: -1}})).Decode(&last)
	if err == nil && time.Since(last.CreatedAt) < otpMinGap {
		log.Info().Str("email", email).Msg("otp-request: rate-limited")
		return nil
	}

	// Pick the right surface for the magic link. Explicit hint wins,
	// otherwise fall back to role defaults so the link lands on the
	// panel the user can actually log into.
	resolvedSurface := normaliseSurface(surface)
	if resolvedSurface == "" {
		if user.Role == "vendor_owner" {
			resolvedSurface = surfaceWHM
		} else {
			resolvedSurface = surfaceUserPanel
		}
	}

	code, err := generateOTPCode()
	if err != nil {
		return err
	}
	now := time.Now()
	doc := models.OTPRequest{
		Email:     email,
		CodeHash:  sha256Hex(code),
		Surface:   resolvedSurface,
		IP:        ip,
		UserAgent: userAgent,
		Attempts:  0,
		Used:      false,
		CreatedAt: now,
		ExpiresAt: now.Add(otpTTL),
	}
	if _, err := otps.InsertOne(ctx, doc); err != nil {
		return err
	}

	magicURL := s.otpMagicURL(email, code, resolvedSurface)

	if s.m == nil || !s.m.Enabled() {
		// Mailer disabled — log the code so a fresh-install operator
		// can still get in via journalctl. This mirrors the ForgotPassword
		// fallback.
		log.Warn().Str("email", email).Msg("otp-request: SMTP not configured — code only in stderr")
		fmt.Printf("[auth] OTP login code (mailer disabled) for %s: %s (expires in %d min, URL: %s)\n",
			email, code, int(otpTTL/time.Minute), magicURL)
		return nil
	}

	subject, text, htmlBody, err := mailer.BuildOTPEmail(mailer.OTPEmailData{
		Name:       user.Name,
		Email:      user.Email,
		Role:       user.Role,
		Code:       code,
		MagicURL:   magicURL,
		ExpiresMin: int(otpTTL / time.Minute),
		PanelName:  "Betazen Server Panel",
		IP:         ip,
		UserAgent:  userAgent,
	})
	if err != nil {
		return err
	}
	if err := s.m.Send(ctx, mailer.Message{
		To:      email,
		Subject: subject,
		Text:    text,
		HTML:    htmlBody,
	}); err != nil {
		log.Error().Err(err).
			Str("email", email).
			Str("smtp_host", s.m.Config().Host).
			Int("smtp_port", s.m.Config().Port).
			Msg("otp-request: SMTP send failed")
		return err
	}
	log.Info().Str("email", email).Str("surface", resolvedSurface).Msg("otp-request: code sent")
	return nil
}

// VerifyOTP consumes a pending OTP. On success it issues the JWT pair
// the same way password login does and records a LoginSession. Errors
// are deliberately vague ("invalid or expired code") to prevent an
// attacker from distinguishing between wrong-code vs. wrong-email.
func (s *AuthService) VerifyOTP(ctx context.Context, email, code, ip, userAgent string) (*models.LoginResponse, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	code = strings.TrimSpace(code)
	if email == "" || code == "" {
		return nil, errors.New("invalid or expired code")
	}

	otps := s.db.Collection(database.ColOTPRequests)
	codeHash := sha256Hex(code)

	// Atomic consume: find a matching unused, unexpired OTP and mark
	// it used in a single update. This prevents two concurrent
	// verifications from both succeeding.
	now := time.Now()
	filter := bson.M{
		"email":      email,
		"code_hash":  codeHash,
		"used":       false,
		"expires_at": bson.M{"$gt": now},
		"attempts":   bson.M{"$lt": otpMaxAttempts},
	}
	update := bson.M{
		"$set": bson.M{"used": true, "used_at": now},
	}
	res := otps.FindOneAndUpdate(ctx, filter, update)
	var otp models.OTPRequest
	if err := res.Decode(&otp); err != nil {
		// Mismatch — bump the attempts counter on any pending OTP for
		// this email so enough bad guesses eventually invalidate it.
		_, _ = otps.UpdateOne(ctx, bson.M{
			"email":      email,
			"used":       false,
			"expires_at": bson.M{"$gt": now},
		}, bson.M{"$inc": bson.M{"attempts": 1}})
		return nil, errors.New("invalid or expired code")
	}

	// Look up the user and issue tokens. We go through the same path
	// as password login so the response shape matches.
	users := s.db.Collection(database.ColUsers)
	var user models.User
	if err := users.FindOne(ctx, bson.M{
		"email":      email,
		"deleted_at": nil,
		"is_active":  true,
	}).Decode(&user); err != nil {
		return nil, errors.New("account not available")
	}

	perms := user.Permissions
	if len(perms) == 0 {
		perms = constants.DefaultPermissions[user.Role]
	}
	refreshExpiry := s.cfg.JWTRefreshExpiry

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
	refreshToken, err := jwt.GenerateRefreshToken()
	if err != nil {
		return nil, errors.New("failed to generate refresh token")
	}

	refreshExpiresAt := now.Add(refreshExpiry)
	_, _ = users.UpdateByID(ctx, user.ID, bson.M{
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

	// Record the session (geoip + UA parse happens in the goroutine
	// so the HTTP response isn't blocked on ip-api.com). Fire-and-
	// forget with a fresh context — the request-scoped one is cancelled
	// by the time this goroutine runs.
	go s.RecordLoginSession(context.Background(), &user, "otp", ip, userAgent)

	return &models.LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int(s.cfg.JWTAccessExpiry.Seconds()),
		TokenType:    "Bearer",
		User:         &user,
	}, nil
}

// ─── Login session audit ────────────────────────────────────────────
//
// RecordLoginSession writes a row to login_sessions with the caller's
// origin so the Account → Sessions page can render "recent logins".
// Safe to call in a goroutine; takes its own context so the caller's
// request ctx being cancelled doesn't abort the DB write.

func (s *AuthService) RecordLoginSession(ctx context.Context, user *models.User, method, ip, userAgent string) {
	if user == nil {
		return
	}
	// Give the background lookup a hard ceiling so a slow ip-api
	// response can't keep the goroutine alive past login.
	lookupCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	geo, _ := geoip.Lookup(lookupCtx, ip) // best-effort; blank result is fine
	ua := uaparse.Parse(userAgent)

	now := time.Now()
	doc := models.LoginSession{
		UserID:    user.ID,
		Email:     user.Email,
		Role:      user.Role,
		Method:    method,
		IP:        ip,
		Country:   geo.Country,
		Region:    geo.Region,
		City:      geo.City,
		UserAgent: userAgent,
		Browser:   ua.Browser,
		OS:        ua.OS,
		Device:    ua.Device,
		LoginAt:   now,
	}
	insertCtx, insertCancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer insertCancel()
	if _, err := s.db.Collection(database.ColLoginSessions).InsertOne(insertCtx, doc); err != nil {
		log.Warn().Err(err).Str("user_id", user.ID.Hex()).Msg("record-login-session: insert failed")
	}
}

// ListSessions returns the caller's recent login sessions, newest
// first, capped at `limit`. Used by the Account → Sessions page so
// the user can eyeball "was that me?" and flag anything suspicious.
func (s *AuthService) ListSessions(ctx context.Context, userID string, limit int) ([]models.LoginSession, error) {
	oid, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return nil, errors.New("invalid user id")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	cur, err := s.db.Collection(database.ColLoginSessions).Find(ctx,
		bson.M{"user_id": oid},
		options.Find().
			SetSort(bson.D{{Key: "login_at", Value: -1}}).
			SetLimit(int64(limit)),
	)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []models.LoginSession
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []models.LoginSession{}
	}
	return out, nil
}
