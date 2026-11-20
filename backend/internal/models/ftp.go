package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type FTPAccount struct {
	ID       primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Username string             `bson:"username" json:"username"`
	Domain   string             `bson:"domain" json:"domain"`
	HomeDir  string             `bson:"home_dir" json:"home_dir"`
	// User is the system (linux) account that owns this FTP user — i.e. the
	// owner of Domain. It is the tenant-scoping key (mirrors Database.Owner)
	// and the field user_service / transfer-sync already query by ("user").
	// Older rows written before v3.1.287 may have it empty; readers fall back
	// to Domain ownership and heal it on access.
	User      string    `bson:"user" json:"user"`
	IsRoot    bool      `bson:"is_root" json:"is_root"` // root FTP accounts cannot be deleted
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// FTPAccountView is the API shape: the stored account plus the connection
// details the UI needs (host/port), and — only in the response to a create or
// password-reset — the plaintext password (Pure-FTPd stores only a hash, so it
// is shown exactly once and never persisted or listable afterwards).
type FTPAccountView struct {
	FTPAccount `bson:",inline"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Password   string `json:"password,omitempty"`
}

// CreateFTPRequest adds an additional FTP account under a domain the caller
// owns. Username is an optional suffix (it is prefixed with the owner +"_" to
// namespace it, like databases); Password is optional (generated when blank).
type CreateFTPRequest struct {
	Domain   string `json:"domain" validate:"required"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// UpdateFTPPasswordRequest resets an FTP account's password. Password is
// optional — blank means "generate a strong one and return it".
type UpdateFTPPasswordRequest struct {
	Password string `json:"password"`
}
