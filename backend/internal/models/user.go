package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type User struct {
	ID              primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Username        string             `bson:"username" json:"username"`
	Email           string             `bson:"email" json:"email"`
	Password        string             `bson:"password" json:"-"`
	Name            string             `bson:"name" json:"name"`
	Role            string             `bson:"role" json:"role"`
	Permissions     []string           `bson:"permissions" json:"permissions"`
	Domains         []string           `bson:"domains" json:"domains"`
	IsActive        bool               `bson:"is_active" json:"is_active"`
	TwoFactorEnabled bool             `bson:"two_factor_enabled" json:"two_factor_enabled"`
	TwoFactorSecret  string           `bson:"two_factor_secret" json:"-"`
	RecoveryCodes    []string         `bson:"recovery_codes" json:"-"`
	RefreshToken     string           `bson:"refresh_token" json:"-"`
	FailedLogins     int              `bson:"failed_logins" json:"-"`
	LockedUntil      *time.Time       `bson:"locked_until" json:"-"`
	LastLogin        *time.Time       `bson:"last_login" json:"last_login"`
	CreatedAt        time.Time        `bson:"created_at" json:"created_at"`
	UpdatedAt        time.Time        `bson:"updated_at" json:"updated_at"`
}

type CreateUserRequest struct {
	Email       string   `json:"email" validate:"required,email"`
	Password    string   `json:"password" validate:"required,min=8"`
	Name        string   `json:"name" validate:"required"`
	Role        string   `json:"role" validate:"required,oneof=vendor_owner vendor_admin developer support customer"`
	Permissions []string `json:"permissions"`
	Domains     []string `json:"domains"`
	Notify      bool     `json:"notify"`
}

type UpdateUserRequest struct {
	Name        *string  `json:"name"`
	Role        *string  `json:"role" validate:"omitempty,oneof=vendor_owner vendor_admin developer support customer"`
	Permissions []string `json:"permissions"`
	Domains     []string `json:"domains"`
	IsActive    *bool    `json:"is_active"`
}

type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
	TOTPCode string `json:"totp_code"`
}

type LoginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	User         *User  `json:"user"`
}
