package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Domain struct {
	ID               primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Domain           string             `bson:"domain" json:"domain"`
	User             string             `bson:"user" json:"user"`
	Password         string             `bson:"password" json:"-"`
	PHPVersion       string             `bson:"php_version" json:"php_version"`
	DiskQuotaMB      int                `bson:"disk_quota_mb" json:"disk_quota_mb"`
	BandwidthLimitGB int                `bson:"bandwidth_limit_gb" json:"bandwidth_limit_gb"`
	MaxDatabases     int                `bson:"max_databases" json:"max_databases"`
	MaxEmailAccounts int                `bson:"max_email_accounts" json:"max_email_accounts"`
	MaxSubdomains    int                `bson:"max_subdomains" json:"max_subdomains"`
	MaxApps          int                `bson:"max_apps" json:"max_apps"`
	SSLActive        bool               `bson:"ssl_active" json:"ssl_active"`
	SSLExpires       *time.Time         `bson:"ssl_expires" json:"ssl_expires"`
	ForceSSL         bool               `bson:"force_ssl" json:"force_ssl"`
	Status           string             `bson:"status" json:"status"`
	// Registration / whois details — operator-entered so we always have
	// them even for TLDs that return no public whois. A periodic whois
	// refresh job can update these if the admin wants automation, but
	// manual entry is the source of truth.
	Registrar     string     `bson:"registrar" json:"registrar"`
	RegisteredOn  *time.Time `bson:"registered_on" json:"registered_on"`
	ExpiresOn     *time.Time `bson:"expires_on" json:"expires_on"`
	AutoRenew     bool       `bson:"auto_renew" json:"auto_renew"`
	Nameservers   []string   `bson:"nameservers" json:"nameservers"`
	WhoisSyncedAt *time.Time `bson:"whois_synced_at" json:"whois_synced_at"`
	// ExpiryNoticeStage is the smallest days-left bucket the expiry
	// cron has already emailed the vendor about. The stage ladder is
	// 30 → 21 → 14 → 7 → 5 → 3 → 2 → 1; the cron only sends when the
	// current days-left is at or below a bucket it hasn't yet marked.
	// Resets to 0 when ExpiresOn is cleared or pushed further out,
	// so renewing a domain silently re-arms the warnings.
	ExpiryNoticeStage int `bson:"expiry_notice_stage,omitempty" json:"expiry_notice_stage,omitempty"`
	// Preflight-stamped fields. Populated by RunPreflight on Create and
	// on every /:id/recheck call so the operator can see at a glance
	// whether the domain is still pointed at this server. ResolvedIP is
	// the first A record returned at the time of the last check.
	ResolvedIP      string     `bson:"resolved_ip,omitempty" json:"resolved_ip,omitempty"`
	DomainType      string     `bson:"domain_type,omitempty" json:"domain_type,omitempty"`
	IPMatchesServer bool       `bson:"ip_matches_server,omitempty" json:"ip_matches_server,omitempty"`
	LastCheckedAt   *time.Time `bson:"last_checked_at,omitempty" json:"last_checked_at,omitempty"`
	CreatedAt       time.Time  `bson:"created_at" json:"created_at"`
	UpdatedAt       time.Time  `bson:"updated_at" json:"updated_at"`
	// OwnerEmail is the *vendor's* registered email — the tenant root
	// for whichever user owns this domain. Computed at list time
	// (DomainService.EnrichOwnerEmails) and never persisted to Mongo
	// (`bson:"-"`), so the source of truth stays the User document.
	// Used by the SSL page to autofill the "Email" field on the
	// Issue Certificate modal without a per-domain HTTP round-trip.
	OwnerEmail string `bson:"-" json:"owner_email,omitempty"`
}

type CreateDomainRequest struct {
	Domain           string   `json:"domain" validate:"required"`
	User             string   `json:"user" validate:"required"`
	PHPVersion       string   `json:"php_version" validate:"required,oneof=7.4 8.0 8.1 8.2 8.3"`
	ServerIP         string   `json:"server_ip"`
	Nameservers      []string `json:"nameservers"`
	DiskQuotaMB      int      `json:"disk_quota_mb"`
	BandwidthLimitGB int      `json:"bandwidth_limit_gb"`
	MaxDatabases     int      `json:"max_databases"`
	MaxEmailAccounts int      `json:"max_email_accounts"`
	MaxSubdomains    int      `json:"max_subdomains"`
	MaxApps          int      `json:"max_apps"`
	// Registration details. All optional — operators who don't track
	// their registrar in the panel can leave them blank; the domain
	// just won't show up in the dashboard expiries widget.
	// Dates are RFC3339 strings (YYYY-MM-DD accepted too — the handler
	// parses both). Empty string = nil.
	Registrar     string `json:"registrar"`
	RegisteredOn  string `json:"registered_on"`
	ExpiresOn     string `json:"expires_on"`
	AutoRenew     bool   `json:"auto_renew"`
}

// UpdateRegistrationRequest patches just the registration/whois fields
// on an existing domain — used by the "Edit registration" action so
// resource-limit tweaks and registration edits stay on distinct modals.
type UpdateRegistrationRequest struct {
	Registrar    string   `json:"registrar"`
	RegisteredOn string   `json:"registered_on"`
	ExpiresOn    string   `json:"expires_on"`
	AutoRenew    *bool    `json:"auto_renew"`
	Nameservers  []string `json:"nameservers"`
}

type Subdomain struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	DomainID     primitive.ObjectID `bson:"domain_id" json:"domain_id"`
	Subdomain    string             `bson:"subdomain" json:"subdomain"`
	DocumentRoot string             `bson:"document_root" json:"document_root"`
	PHPVersion   string             `bson:"php_version" json:"php_version"`
	CreatedAt    time.Time          `bson:"created_at" json:"created_at"`
}

type DomainAlias struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	DomainID    primitive.ObjectID `bson:"domain_id" json:"domain_id"`
	AliasDomain string             `bson:"alias_domain" json:"alias_domain"`
	CreatedAt   time.Time          `bson:"created_at" json:"created_at"`
}

type DomainRedirect struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	DomainID   primitive.ObjectID `bson:"domain_id" json:"domain_id"`
	SourcePath string             `bson:"source_path" json:"source_path"`
	TargetURL  string             `bson:"target_url" json:"target_url"`
	Type       string             `bson:"type" json:"type"`
	MatchType  string             `bson:"match_type" json:"match_type"`
	CreatedAt  time.Time          `bson:"created_at" json:"created_at"`
}
