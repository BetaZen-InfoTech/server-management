package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// APIToken is a GitHub-style personal access token used to call /api/v1/whm/api/*
// or /api/v1/cpanel/api/* without a JWT login. The plaintext secret is shown to
// the operator exactly once, at creation time, and is never persisted. What is
// persisted is the public token id (used to look up the row) and a bcrypt hash
// of the secret (used to verify the bearer string on every authenticated call).
type APIToken struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	TokenID    string             `bson:"token_id" json:"token_id"`
	SecretHash string             `bson:"secret_hash" json:"-"`
	Prefix     string             `bson:"prefix" json:"prefix"`

	Name        string `bson:"name" json:"name"`
	Description string `bson:"description,omitempty" json:"description,omitempty"`

	OwnerKind   string             `bson:"owner_kind" json:"owner_kind"`
	OwnerUserID primitive.ObjectID `bson:"owner_user_id" json:"owner_user_id"`
	TenantID    primitive.ObjectID `bson:"tenant_id" json:"tenant_id"`

	PinnedVendorID primitive.ObjectID `bson:"pinned_vendor_id,omitempty" json:"pinned_vendor_id,omitempty"`

	Scopes []string `bson:"scopes" json:"scopes"`

	IPAllowlist []string `bson:"ip_allowlist,omitempty" json:"ip_allowlist,omitempty"`

	ExpiresAt *time.Time `bson:"expires_at,omitempty" json:"expires_at,omitempty"`

	Status string `bson:"status" json:"status"`

	LastUsedAt *time.Time `bson:"last_used_at,omitempty" json:"last_used_at,omitempty"`
	LastUsedIP string     `bson:"last_used_ip,omitempty" json:"last_used_ip,omitempty"`
	UsageCount int64      `bson:"usage_count" json:"usage_count"`

	FailedAuthCount int        `bson:"failed_auth_count,omitempty" json:"failed_auth_count,omitempty"`
	LockedUntil     *time.Time `bson:"locked_until,omitempty" json:"locked_until,omitempty"`

	CreatedAt time.Time  `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time  `bson:"updated_at" json:"updated_at"`
	RevokedAt *time.Time `bson:"revoked_at,omitempty" json:"revoked_at,omitempty"`

	// 3.1.105 — transient owner-enrichment fields. NOT persisted to
	// mongo (`bson:"-"`) because the source of truth is the users
	// collection keyed by OwnerUserID — storing them on every token
	// would either bloat the DB or go stale when a vendor renames.
	// APITokenService.List populates them in-memory after the find
	// so the Developer UI can render an Owner column without a
	// second round-trip per row.
	//
	// OwnerKind already tells us whether the row was created by a
	// platform admin ("platform") vs a vendor ("user"). When kind
	// is "platform", OwnerName is set to "Platform admin" and
	// OwnerUsername stays blank. When kind is "user", both fields
	// reflect the vendor's User document. Missing user (deleted
	// vendor) leaves both empty; UI shows a "—" placeholder.
	OwnerName     string `bson:"-" json:"owner_name,omitempty"`
	OwnerUsername string `bson:"-" json:"owner_username,omitempty"`
}

// CreateAPITokenRequest is the body shape posted to POST /api-tokens.
// PinnedVendorID is only honoured when the caller is vendor_owner — any other
// role gets that field silently ignored at the service layer.
type CreateAPITokenRequest struct {
	Name           string   `json:"name" validate:"required,min=1,max=80"`
	Description    string   `json:"description"`
	Scopes         []string `json:"scopes" validate:"required,min=1"`
	ExpiresInDays  int      `json:"expires_in_days"`
	IPAllowlist    []string `json:"ip_allowlist"`
	PinnedVendorID string   `json:"pinned_vendor_id"`
}

// IssuedAPIToken is the response shape returned exactly once on creation. The
// plaintext secret is in PlaintextToken; clients MUST persist it locally before
// dismissing the dialog because the panel cannot recover it later.
type IssuedAPIToken struct {
	Token          *APIToken `json:"token"`
	PlaintextToken string    `json:"plaintext_token"`
}

// APITokenScope catalogues every scope the panel knows how to authorise. The
// Developer page reads this list to render its scope picker; the middleware
// reads it via FindScope to enforce route-level requirements.
type APITokenScope struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Group       string `json:"group"`
	Permission  string `json:"-"`
}

// AllAPITokenScopes is the catalogue. Keep in sync with the route table in
// routes/api_routes.go — every programmatic endpoint declares one of these.
var AllAPITokenScopes = []APITokenScope{
	{Key: "domain:read", Label: "Read domains", Description: "List and inspect domains", Group: "Domain", Permission: "domain.view"},
	{Key: "domain:write", Label: "Manage domains", Description: "Create, update, and delete domains", Group: "Domain", Permission: "domain.create"},
	{Key: "email:read", Label: "Read mailboxes", Description: "List mailboxes, forwarders, and per-mailbox stats", Group: "Email", Permission: "email.view"},
	{Key: "email:write", Label: "Manage mailboxes", Description: "Create or delete mailboxes and forwarders", Group: "Email", Permission: "email.manage"},
	{Key: "email:webmail", Label: "Mint webmail links", Description: "Generate single-sign-on links to Roundcube", Group: "Email", Permission: "email.view"},
	// email:password is intentionally separate from email:write. Password
	// rotation is a higher-trust operation than mailbox provisioning —
	// an integrator might want a service account that can create new
	// mailboxes (e.g. for a self-serve signup flow) without also being
	// able to lock existing users out by rotating their passwords. Same
	// shape as the dedicated email:webmail scope above.
	{Key: "email:password", Label: "Reset mailbox passwords", Description: "Reset the password on an existing mailbox (rewrites dovecot users + webmail SSO secret)", Group: "Email", Permission: "email.manage"},
	{Key: "ssl:read", Label: "Read SSL certificates", Description: "List installed certificates and their status", Group: "SSL", Permission: "ssl.manage"},
	{Key: "ssl:write", Label: "Issue and force SSL", Description: "Issue Let's Encrypt certificates and toggle Force HTTPS", Group: "SSL", Permission: "ssl.manage"},
	{Key: "deploy:read", Label: "Read deploy projects", Description: "List Deploy Software projects and their services", Group: "Deploy", Permission: "deploy.manage"},
	{Key: "deploy:link", Label: "Link domains to services", Description: "Attach or detach domains on Deploy Software services", Group: "Deploy", Permission: "deploy.manage"},
	{Key: "webhook:manage", Label: "Manage webhooks", Description: "Create or delete outbound webhook endpoints", Group: "Developer", Permission: "server.manage"},
	// guest:create mints one-time, 30-minute, browser-locked guest links
	// for a single domain (email + restricted DNS). Gated on email.manage —
	// the floor capability a guest link can hand out.
	{Key: "guest:create", Label: "Mint guest links", Description: "Create one-time, time-limited, browser-locked guest links to manage a single domain's email (and DNS for main domains)", Group: "Guest", Permission: "email.manage"},
	// Cloudflare — connect a domain to the panel's Cloudflare account and read
	// its zone status + assigned nameservers. cloudflare:read is the one a
	// reseller integration needs after adding a domain: fetch the Cloudflare
	// nameservers to hand back to the end customer for their registrar.
	{Key: "cloudflare:read", Label: "Read Cloudflare zone", Description: "Read a domain's Cloudflare zone status and assigned nameservers", Group: "Cloudflare", Permission: "domain.view"},
	{Key: "cloudflare:write", Label: "Connect + sync Cloudflare", Description: "Create/reuse a domain's Cloudflare zone and push its DNS records", Group: "Cloudflare", Permission: "domain.manage"},
}

// FindScope returns the catalogue entry for a scope key; ok=false for unknown.
func FindScope(key string) (APITokenScope, bool) {
	for _, s := range AllAPITokenScopes {
		if s.Key == key {
			return s, true
		}
	}
	return APITokenScope{}, false
}

const (
	APITokenStatusActive  = "active"
	APITokenStatusExpired = "expired"
	APITokenStatusRevoked = "revoked"

	APITokenOwnerKindOwner  = "owner"
	APITokenOwnerKindVendor = "vendor"
)
