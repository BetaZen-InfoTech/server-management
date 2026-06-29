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
	// DocumentRoot overrides the nginx vhost's `root` directive.
	//
	// Empty (the default) → nginx serves from
	//   /home/<user>/domains/<domain>/public_html
	// which is the cPanel-style layout used by every domain pre-3.1.83.
	//
	// Non-empty → nginx serves from this absolute path. Used to point a
	// domain at a Laravel project's /public folder, a Next.js export
	// directory, or any other docroot the operator chooses. The change
	// rewrites both the HTTP and SSL vhost files, then reloads nginx;
	// the value is also replayed by the transfer pipeline's
	// healMissingVhosts so it survives server-to-server migration.
	DocumentRoot string `bson:"document_root,omitempty" json:"document_root,omitempty"`
	// ProxyServiceID + ProxyPort make a domain a DURABLE reverse-proxy
	// to a Deploy-Software project service. When ProxyServiceID is set,
	// this domain gets its OWN nginx vhost (server_name <d> www.<d>
	// cname.<d>) reverse-proxying to 127.0.0.1:ProxyPort — it is NOT
	// merged into the service's primary server_name. This is the v3.1.114
	// replacement for the fragile alias_domains mechanism:
	//
	//   - alias_domains lived in an array ON the service row, so any
	//     service edit that re-sent a stale list silently dropped
	//     domains (the "added via API, works, doesn't show" bug). The
	//     binding now lives on the Domain row, so a service edit can't
	//     touch it.
	//   - the upstream port had to be re-derived via
	//     lookupProjectServiceByDomain (which only matched while the
	//     domain was still in alias_domains), so once the alias was
	//     dropped the SSL/migration/restore flows rebuilt the WRONG
	//     vhost. ProxyPort persists the port, so every vhost flow
	//     (issue/revoke SSL, restore, recheck, migration) rebuilds the
	//     correct reverse-proxy durably with no service lookup.
	//   - each attached domain has its own vhost + cert, so two domains
	//     pointing at one service never collide on server_name (the
	//     "conflicting server name … ignored" warnings).
	//
	// ProxyPort is cached from the service's Port at attach time and is
	// refreshed whenever the service's port changes; ProxyServiceID is
	// the source of truth for the link (used for self-heal + panel
	// grouping). Both empty/0 on a plain file-based domain.
	ProxyServiceID *primitive.ObjectID `bson:"proxy_service_id,omitempty" json:"proxy_service_id,omitempty"`
	ProxyPort      int                 `bson:"proxy_port,omitempty" json:"proxy_port,omitempty"`
	// Source records how the domain row was created so the operator
	// can sort / filter the Domains list by origin. Known values:
	//
	//   "manual"      — single-create via the Add Domain modal
	//                   (default for legacy rows where the field is
	//                   empty, since manual was the only path before
	//                   bulk-upload + the external API existed)
	//   "bulk_upload" — CSV / XLSX bulk-upload row
	//   "api"         — POST /api/v1/external/domains (token-
	//                   authenticated 3rd-party programmatic create)
	//   "transfer"    — restored from a server-to-server transfer
	//                   (transfer pipeline preserves whatever value
	//                   was on the source row; truly origin-less
	//                   transferred rows get this fallback)
	//
	// Stored as a free-form string so future creation paths
	// (Terraform provider, panel-to-panel sync, etc.) can extend the
	// set without a migration. The Domains-page filter dropdown
	// keys on known values and shows unknown values as-is.
	Source string `bson:"source,omitempty" json:"source,omitempty"`
	// Environment is the operator-chosen deployment tier for the domain
	// (and any subdomain — both go through DomainService.Create). Known
	// values: "prod" (default), "dev", "test", "local". Distinct from
	// the structural DomainType field below ("primary" / "subdomain" /
	// "addon") which is computed by preflight — Environment is purely a
	// human label the operator sets so the Domains list can be grouped
	// and filtered by tier. Empty on legacy rows; normalized to "prod"
	// on Create and treated as "prod" by the UI for badge purposes.
	Environment string `bson:"environment,omitempty" json:"environment,omitempty"`
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
	// SetupWarnings carry transient, post-create status from the
	// zone / mail / SSL setup pipelines. NEVER persisted to Mongo
	// (`bson:"-"`) — only stamped on the in-memory Domain returned
	// by Create() so the caller (single handler, bulk-upload row,
	// programmatic API client) can surface "zone created but mail
	// setup failed" to the operator instead of swallowing the
	// stderr-only warnings the create flow used to emit. Empty slice
	// = clean create. Each entry is a single human-readable line
	// the UI can show verbatim.
	SetupWarnings []string `bson:"-" json:"setup_warnings,omitempty"`
	// AdminMailboxPassword is the auto-generated password for the
	// admin@<domain> mailbox the create flow stamps on every new
	// domain. NEVER persisted (`bson:"-"`) — surfaced ONLY on the
	// fresh-create response so the operator can save it before the
	// modal closes. Empty when the domain was loaded from Mongo or
	// when the auto-mailbox creation failed (in which case the
	// reason lands in SetupWarnings).
	AdminMailboxPassword string `bson:"-" json:"admin_mailbox_password,omitempty"`
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
	// Optional vhost-level overrides.
	// DocumentRoot lets the operator point a new domain at a custom
	// docroot at create time (e.g. Laravel's /public folder).
	// Empty = use the cPanel-default /home/<user>/domains/<domain>/public_html.
	DocumentRoot string `json:"document_root"`
	// Source is stamped by the caller (manual handler / bulk-upload
	// service / programmatic API handler) BEFORE invoking
	// DomainService.Create, so the service layer doesn't have to
	// know its caller. Empty falls back to "manual" inside Create.
	// See the Source field on the Domain struct above for value
	// catalogue.
	Source string `json:"source"`
	// Environment is the deployment tier the caller wants this domain
	// tagged with: "prod" (default), "dev", "test", or "local". Empty
	// or unrecognised values normalize to "prod" inside Create. Sent by
	// the Add Domain modal (WHM + User Panel) and accepted verbatim by
	// the programmatic create endpoint so an integrator can stamp the
	// tier at provision time.
	Environment string `json:"environment"`
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
