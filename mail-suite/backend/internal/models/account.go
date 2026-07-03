package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// MailAccount is a single mailbox attached to a User.
// Provider "betazen" = our local Postfix/Dovecot; "imap" = external (Gmail-via-app-password etc).
type MailAccount struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID     primitive.ObjectID `bson:"user_id" json:"user_id"`
	DisplayName string            `bson:"display_name" json:"display_name"`
	Address    string             `bson:"address" json:"address"`
	Provider   string             `bson:"provider" json:"provider"`

	// IMAP/SMTP — both filled in by the server when adding a betazen mailbox,
	// supplied by the user when adding an external one.
	IMAPHost string `bson:"imap_host" json:"imap_host"`
	IMAPPort int    `bson:"imap_port" json:"imap_port"`
	IMAPSSL  bool   `bson:"imap_ssl" json:"imap_ssl"`
	SMTPHost string `bson:"smtp_host" json:"smtp_host"`
	SMTPPort int    `bson:"smtp_port" json:"smtp_port"`
	SMTPSSL  bool   `bson:"smtp_ssl" json:"smtp_ssl"`
	Username string `bson:"username" json:"username"`

	// Encrypted; never returned to the client. For now we store the password
	// in plaintext as the deployment is single-tenant and the DB is private;
	// a future ticket will swap in AES-GCM with a per-app key.
	Secret string `bson:"secret" json:"-"`

	// Optional default identity / signature for "From"
	DefaultSignatureID primitive.ObjectID `bson:"default_signature_id,omitempty" json:"default_signature_id,omitempty"`

	IsPrimary bool      `bson:"is_primary" json:"is_primary"`
	Color     string    `bson:"color,omitempty" json:"color,omitempty"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

type AddAccountRequest struct {
	DisplayName string `json:"display_name" validate:"required"`
	Address     string `json:"address" validate:"required,email"`
	Provider    string `json:"provider" validate:"required,oneof=betazen imap"`

	IMAPHost string `json:"imap_host"`
	IMAPPort int    `json:"imap_port"`
	IMAPSSL  bool   `json:"imap_ssl"`
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	SMTPSSL  bool   `json:"smtp_ssl"`
	Username string `json:"username"`
	Password string `json:"password" validate:"required"`

	Color string `json:"color"`
}

// TestAccountRequest validates mailbox credentials against the real IMAP/SMTP
// servers WITHOUT saving anything — powers the "Test connection" button in the
// external-mailbox setup so a broken host/port/password is caught before the
// account is added. Display name / color are irrelevant here so they're omitted.
type TestAccountRequest struct {
	Address  string `json:"address" validate:"required,email"`
	Provider string `json:"provider" validate:"required,oneof=betazen imap"`
	Username string `json:"username"`
	Password string `json:"password" validate:"required"`

	IMAPHost string `json:"imap_host"`
	IMAPPort int    `json:"imap_port"`
	IMAPSSL  bool   `json:"imap_ssl"`
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	SMTPSSL  bool   `json:"smtp_ssl"`
}
