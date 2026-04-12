package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type User struct {
	ID              primitive.ObjectID  `bson:"_id,omitempty" json:"id"`
	Username        string              `bson:"username" json:"username"`
	Email           string              `bson:"email" json:"email"`
	Password        string              `bson:"password" json:"-"`
	Name            string              `bson:"name" json:"name"`
	Role            string              `bson:"role" json:"role"`
	// TenantID is the ObjectID of the tenant root account this user belongs to.
	// For vendor_owner / vendor_admin (tenant roots) it equals the user's own _id.
	// For vendor_staff / customer / developer / support created inside a tenant,
	// it equals the parent vendor's _id. Used by tenant_scope to filter list
	// queries so vendors only see their own data.
	TenantID        primitive.ObjectID  `bson:"tenant_id,omitempty" json:"tenant_id,omitempty"`
	// ParentUserID is the immediate creator. nil for tenant roots.
	ParentUserID    *primitive.ObjectID `bson:"parent_user_id,omitempty" json:"parent_user_id,omitempty"`
	PackageID       *primitive.ObjectID `bson:"package_id,omitempty" json:"package_id"`
	PackageName     string              `bson:"package_name" json:"package_name"`
	Permissions     []string            `bson:"permissions" json:"permissions"`
	Domains         []string            `bson:"domains" json:"domains"`
	IsActive        bool               `bson:"is_active" json:"is_active"`
	TwoFactorEnabled bool             `bson:"two_factor_enabled" json:"two_factor_enabled"`
	TwoFactorSecret  string           `bson:"two_factor_secret" json:"-"`
	RecoveryCodes    []string         `bson:"recovery_codes" json:"-"`
	RefreshToken     string           `bson:"refresh_token" json:"-"`
	RefreshExpiresAt *time.Time       `bson:"refresh_expires_at" json:"-"`
	FailedLogins     int              `bson:"failed_logins" json:"-"`
	LockedUntil      *time.Time       `bson:"locked_until" json:"-"`
	LastLogin        *time.Time       `bson:"last_login" json:"last_login"`
	LastLoginIP      string           `bson:"last_login_ip" json:"last_login_ip"`
	CreatedAt        time.Time        `bson:"created_at" json:"created_at"`
	UpdatedAt        time.Time        `bson:"updated_at" json:"updated_at"`
}

type CreateUserRequest struct {
	Email       string   `json:"email" validate:"required,email"`
	Password    string   `json:"password" validate:"required,min=8"`
	Name        string   `json:"name" validate:"required"`
	Role        string   `json:"role" validate:"required,oneof=vendor_owner vendor_admin vendor_staff developer support customer"`
	PackageID   string   `json:"package_id"`
	Permissions []string `json:"permissions"`
	Domains     []string `json:"domains"`
	Notify      bool     `json:"notify"`
}

type UpdateUserRequest struct {
	Name        *string  `json:"name"`
	Role        *string  `json:"role" validate:"omitempty,oneof=vendor_owner vendor_admin vendor_staff developer support customer"`
	Permissions []string `json:"permissions"`
	Domains     []string `json:"domains"`
	IsActive    *bool    `json:"is_active"`
}

type LoginRequest struct {
	Email      string `json:"email" validate:"required,email"`
	Password   string `json:"password" validate:"required"`
	TOTPCode   string `json:"totp_code"`
	RememberMe bool   `json:"remember_me"`
}

type LoginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	User         *User  `json:"user"`
}
