package jwt

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UserID      string   `json:"user_id"`
	Email       string   `json:"email"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	// TenantID is the hex ObjectID of the tenant root account this user belongs
	// to. Used by the auth middleware to populate c.Locals("tenant_id") which
	// every list/get handler reads to scope queries. Empty string for legacy
	// tokens issued before multi-tenancy — handlers must fall back to user_id
	// in that case.
	TenantID string `json:"tenant_id,omitempty"`
	// IsSuperAdmin promotes the holder to "bypass every permission check"
	// within their panel scope. Checked by RequirePermission middleware so
	// a super-admin vendor_admin gets full access to their tenant even if
	// someone forgot to add a permission on a new route.
	IsSuperAdmin bool `json:"is_super_admin,omitempty"`
	// ImpersonatedBy is set when an admin is logged in *as* another user
	// via the impersonation flow. Empty on normal tokens. Handlers can use
	// this for audit logging / showing an "Impersonating as X" banner.
	ImpersonatedBy string `json:"impersonated_by,omitempty"`
	jwtlib.RegisteredClaims
}

func GenerateAccessToken(secret string, expiry time.Duration, userID, email, role, tenantID string, perms []string) (string, error) {
	return GenerateAccessTokenFull(secret, expiry, userID, email, role, tenantID, perms, false, "")
}

// GenerateAccessTokenFull is the full-flagged variant used by the auth +
// impersonation flows. All other call sites go through the compact
// GenerateAccessToken and accept defaults (is_super_admin=false,
// impersonated_by="").
func GenerateAccessTokenFull(secret string, expiry time.Duration, userID, email, role, tenantID string, perms []string, isSuperAdmin bool, impersonatedBy string) (string, error) {
	claims := Claims{
		UserID:         userID,
		Email:          email,
		Role:           role,
		TenantID:       tenantID,
		Permissions:    perms,
		IsSuperAdmin:   isSuperAdmin,
		ImpersonatedBy: impersonatedBy,
		RegisteredClaims: jwtlib.RegisteredClaims{
			ExpiresAt: jwtlib.NewNumericDate(time.Now().Add(expiry)),
			IssuedAt:  jwtlib.NewNumericDate(time.Now()),
			Issuer:    "serverpanel",
		},
	}

	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

func GenerateRefreshToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func ValidateToken(secret, tokenStr string) (*Claims, error) {
	token, err := jwtlib.ParseWithClaims(tokenStr, &Claims{}, func(t *jwtlib.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwtlib.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token")
	}

	return claims, nil
}
