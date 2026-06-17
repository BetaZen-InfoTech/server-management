package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// GuestLink is a one-time, time-limited, browser-locked credential that a
// 3rd-party integrator mints (via the external API, scope guest:create) to
// hand an end-user temporary self-service access to ONE domain — without a
// real account and without exposing any other tenant/domain data.
//
// Lifecycle: minted (status=pending) → first open binds it to one browser and
// starts a 30-minute window (status=active) → window expires or it's swept
// (status=used_expired) / revoked. The plaintext secret is shown exactly once
// in the mint response and never persisted — only its bcrypt hash is stored,
// mirroring the API-token + OTP patterns.
type GuestLink struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	LinkID     string             `bson:"link_id" json:"link_id"`       // public lookup id (indexed, unique)
	SecretHash string             `bson:"secret_hash" json:"-"`         // bcrypt(secret); raw never stored

	Domain   string `bson:"domain" json:"domain"`       // the ONE allowed domain (canonical lowercase)
	LinkType string `bson:"link_type" json:"link_type"` // "email" (subdomain) | "email_dns" (main domain)

	// Email limits captured at mint time and enforced/applied for the
	// whole session.
	MaxMailboxes       int `bson:"max_mailboxes" json:"max_mailboxes"`
	DefaultQuotaMB     int `bson:"default_quota_mb" json:"default_quota_mb"`
	DefaultSendPerHour int `bson:"default_send_per_hour" json:"default_send_per_hour"`

	// Scoping — resolved from the domain's owner at mint time so the guest
	// session runs as the owning tenant (tenant-scoped role for active
	// enforcement). Defense-in-depth behind the handler-level domain forcing.
	OwnerTenantHex string             `bson:"owner_tenant_hex" json:"-"`
	OwnerUserHex   string             `bson:"owner_user_hex" json:"-"`
	OwnerRole      string             `bson:"owner_role" json:"-"`
	MintedByToken  primitive.ObjectID `bson:"minted_by_token,omitempty" json:"-"`

	Status    string    `bson:"status" json:"status"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	// ExpiresAt is the hard deadline by which the link must be FIRST
	// opened (redeem). After it, an un-redeemed link is dead.
	ExpiresAt time.Time `bson:"expires_at" json:"expires_at"`

	// First-open binding (stamped atomically on redeem).
	Redeemed        bool       `bson:"redeemed" json:"redeemed"`
	RedeemedAt      *time.Time `bson:"redeemed_at,omitempty" json:"redeemed_at,omitempty"`
	WindowExpiresAt *time.Time `bson:"window_expires_at,omitempty" json:"window_expires_at,omitempty"` // redeemed_at + 30m
	BindingHash     string     `bson:"binding_hash,omitempty" json:"-"`                                // sha256(browser bind token)
	UAHash          string     `bson:"ua_hash,omitempty" json:"-"`
	RedeemIP        string     `bson:"redeem_ip,omitempty" json:"redeem_ip,omitempty"`
}

// Status + link-type constants.
const (
	GuestLinkStatusPending     = "pending"
	GuestLinkStatusActive      = "active"
	GuestLinkStatusUsedExpired = "used_expired"
	GuestLinkStatusRevoked     = "revoked"

	// GuestLinkTypeEmail = subdomain link (email management only).
	GuestLinkTypeEmail = "email"
	// GuestLinkTypeEmailDNS = main-domain link (email + restricted DNS).
	GuestLinkTypeEmailDNS = "email_dns"
)

// MintGuestLinkRequest is the body posted to POST /api/v1/external/guest-links.
// Only Domain is required; the three limits default to sane values when zero.
type MintGuestLinkRequest struct {
	Domain             string `json:"domain" validate:"required"`
	MaxMailboxes       int    `json:"max_mailboxes"`
	DefaultQuotaMB     int    `json:"default_quota_mb"`
	DefaultSendPerHour int    `json:"default_send_per_hour"`
}

// IssuedGuestLink is the mint response. URL is the full one-time login link the
// integrator redirects the end-user to; it embeds the plaintext token and is
// the only time the secret is visible.
type IssuedGuestLink struct {
	URL       string    `json:"url"`
	LinkType  string    `json:"link_type"`
	Domain    string    `json:"domain"`
	ExpiresAt time.Time `json:"expires_at"`

	// Token is the raw plaintext (gst_<env>_<id>_<secret>) — returned so an
	// integrator who wants to build their own URL has it; the URL field
	// already embeds it.
	Token string `json:"token"`
}
