package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// TransferComponents specifies which items to migrate.
type TransferComponents struct {
	Hostname     bool `bson:"hostname" json:"hostname"`
	DNS          bool `bson:"dns" json:"dns"`
	SSL          bool `bson:"ssl" json:"ssl"`
	Domains      bool `bson:"domains" json:"domains"`
	Files        bool `bson:"files" json:"files"`
	Databases    bool `bson:"databases" json:"databases"`
	EmailData    bool `bson:"email_data" json:"email_data"`
	FTPAccounts  bool `bson:"ftp_accounts" json:"ftp_accounts"`
	CronJobs     bool `bson:"cron_jobs" json:"cron_jobs"`
	Firewall     bool `bson:"firewall" json:"firewall"`
	ServerConfig bool `bson:"server_config" json:"server_config"`
	Software     bool `bson:"software" json:"software"`
	NodeApps     bool `bson:"node_apps" json:"node_apps"` // transfer PM2-managed Node.js apps
	SSHKeys      bool `bson:"ssh_keys" json:"ssh_keys"`   // copy /home/<user>/.ssh/authorized_keys + ssh_keys mongo rows
	Packages     bool `bson:"packages" json:"packages"`   // copy the source's hosting_packages catalog
}

// TransferSelection narrows a transfer to a specific subset of discovered
// items per category. An empty list for a category means "all discovered
// items in that category", keeping the previous category-only API working
// unchanged for clients that don't send a selection.
type TransferSelection struct {
	Domains      []string `bson:"domains,omitempty" json:"domains,omitempty"`
	MySQLDBs     []string `bson:"mysql_dbs,omitempty" json:"mysql_dbs,omitempty"`
	MongoDBs     []string `bson:"mongo_dbs,omitempty" json:"mongo_dbs,omitempty"`
	EmailDomains []string `bson:"email_domains,omitempty" json:"email_domains,omitempty"`
	DNSZones     []string `bson:"dns_zones,omitempty" json:"dns_zones,omitempty"`
	SSLDomains   []string `bson:"ssl_domains,omitempty" json:"ssl_domains,omitempty"`
	FTPUsers     []string `bson:"ftp_users,omitempty" json:"ftp_users,omitempty"`
	CronUsers    []string `bson:"cron_users,omitempty" json:"cron_users,omitempty"`
	NodeApps     []string `bson:"node_apps,omitempty" json:"node_apps,omitempty"`
	// LinuxUsers is the user-centric selection that the wizard sends after
	// the rewrite. When non-empty, the transfer service expands it into
	// the per-resource whitelists above (domains owned by these users,
	// mysql DBs whose name carries one of these prefixes, mailboxes whose
	// /home path matches, FTP accounts whose owner matches, etc). Old
	// clients that still send the per-resource lists keep working.
	LinuxUsers []string `bson:"linux_users,omitempty" json:"linux_users,omitempty"`
}

// TransferStep tracks progress of a single migration step.
type TransferStep struct {
	Name        string     `bson:"name" json:"name"`
	Status      string     `bson:"status" json:"status"` // pending, in_progress, completed, failed, skipped
	StartedAt   *time.Time `bson:"started_at,omitempty" json:"started_at,omitempty"`
	CompletedAt *time.Time `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	Error       string     `bson:"error,omitempty" json:"error,omitempty"`
	Details     string     `bson:"details,omitempty" json:"details,omitempty"`
}

// TransferLog is a single log entry during a transfer.
type TransferLog struct {
	Timestamp time.Time `bson:"timestamp" json:"timestamp"`
	Level     string    `bson:"level" json:"level"` // info, warn, error
	Message   string    `bson:"message" json:"message"`
	Component string    `bson:"component,omitempty" json:"component,omitempty"`
}

// SourceServer holds connection details for the source server.
//
// Two auth modes are supported:
//
//   - "password" (legacy): plaintext root password stored in Password.
//     Kept for migrations from non-Betazen servers where token issuance is
//     not possible.
//   - "token" (preferred): a one-shot transfer token was redeemed against
//     the source panel; the source minted a temporary SSH keypair and
//     returned the private half. PrivateKey holds the PEM bytes; TokenID
//     is the source-side handle so we can revoke after the transfer.
//
// Both Password and PrivateKey are tagged json:"-" so the values never
// leak back through the API. AuthMethod defaults to "password" for old
// records that pre-date the token field.
type SourceServer struct {
	Hostname   string `bson:"hostname" json:"hostname"`
	IP         string `bson:"ip" json:"ip"`
	Port       int    `bson:"port" json:"port"`
	Username   string `bson:"username" json:"username"`
	Password   string `bson:"password" json:"-"`
	Protocol   string `bson:"protocol" json:"protocol"` // ssh
	AuthMethod string `bson:"auth_method,omitempty" json:"auth_method,omitempty"`
	PrivateKey string `bson:"private_key,omitempty" json:"-"`
	TokenID    string `bson:"token_id,omitempty" json:"token_id,omitempty"`
	PanelURL   string `bson:"panel_url,omitempty" json:"panel_url,omitempty"`
}

// LinuxUser is a hosting account that lives under /home on the source.
//
// "Active" means the user has a non-disabled login shell AND the account
// is not locked (`passwd -S` doesn't return L). Inactive users are still
// listed because their files, mailboxes, and crontabs may still be on
// disk and the operator may want to migrate them.
//
// The owned-resource counts (Domains/Mailboxes/etc.) are populated during
// discovery so the wizard can show "vendor1 → 3 sites · 2 mailboxes" next
// to each checkbox without making the operator click in for details.
type LinuxUser struct {
	Username     string `json:"username"`
	UID          int    `json:"uid"`
	Home         string `json:"home"`
	Shell        string `json:"shell"`
	Active       bool   `json:"active"`
	Locked       bool   `json:"locked"`
	Domains      int    `json:"domains"`
	Mailboxes    int    `json:"mailboxes"`
	Databases    int    `json:"databases"`
	FTPUsers     int    `json:"ftp_users"`
	CronJobs     int    `json:"cron_jobs"`
	NodeApps     int    `json:"node_apps"`
	WPSites      int    `json:"wp_sites"`
	HomeBytes    int64  `json:"home_bytes"`
	// PanelManaged is true when the source's Betazen panel mongo has a
	// users row with this linux username — i.e. it's an actual vendor /
	// tenant account the operator created through the panel. False for
	// OS-default accounts under /home (cloud-init's `ubuntu`, manually
	// useradded shell accounts, etc.) which the wizard surfaces but
	// does NOT default-select, since migrating them would just clutter
	// the destination's users collection with rows that were never
	// real vendors.
	PanelManaged bool `json:"panel_managed"`
}

// DomainSetting captures per-domain hosting configuration on the source so
// the wizard can preview it before transfer (PHP version is the most
// commonly-asked-about) and the migration step can recreate the same
// runtime on the destination instead of falling back to defaults.
type DomainSetting struct {
	Domain     string `json:"domain"`
	Owner      string `json:"owner"`        // linux user
	DocumentRoot string `json:"document_root"`
	PHPVersion string `json:"php_version"`  // "8.2", "7.4", or "" if not detected
	HasSSL     bool   `json:"has_ssl"`
	WPInstalled bool  `json:"wp_installed"`
}

// NodeApp is a Node.js / PM2-managed application running on the source.
type NodeApp struct {
	Name       string `json:"name"`
	Cwd        string `json:"cwd"`
	Script     string `json:"script"`
	ExecMode   string `json:"exec_mode"` // fork, cluster
	Instances  int    `json:"instances"`
	NodeVer    string `json:"node_version,omitempty"`
	NpmManager string `json:"npm_manager,omitempty"` // npm, yarn, pnpm
}

// DiscoveredData is what was found on the source server during discovery.
//
// LinuxUsers and DomainSettings were added with the user-centric wizard
// rewrite. The wizard now drives selection off LinuxUsers and treats
// per-user resources (apps, mailboxes, ftp accounts, mysql DBs, etc.) as
// derived — selecting a user implicitly opts that user's whole footprint
// into the transfer.
type DiscoveredData struct {
	Hostname       string          `json:"hostname"`
	ServerType     string          `json:"server_type"` // cpanel, plesk, directadmin, serverpanel, bare
	Domains        []string        `json:"domains"`
	Databases      []string        `json:"databases"`
	MySQLDatabases []string        `json:"mysql_databases"`
	EmailDomains   []string        `json:"email_domains"`
	CronUsers      []string        `json:"cron_users"`
	SSLDomains     []string        `json:"ssl_domains"`
	DNSZones       []string        `json:"dns_zones"`
	FTPUsers       []string        `json:"ftp_users"`
	NodeApps       []NodeApp       `json:"node_apps"`
	LinuxUsers     []LinuxUser     `json:"linux_users"`
	DomainSettings []DomainSetting `json:"domain_settings"`
}

// TransferJob is the main transfer/migration record.
type TransferJob struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Type         string             `bson:"type" json:"type"`                   // full, partial
	Direction    string             `bson:"direction" json:"direction"`         // incoming
	SourceServer SourceServer       `bson:"source_server" json:"source_server"`
	Components   TransferComponents `bson:"components" json:"components"`
	Selection    TransferSelection  `bson:"selection,omitempty" json:"selection,omitempty"`
	Domains      []string           `bson:"domains,omitempty" json:"domains,omitempty"` // specific domains, empty = all
	Status       string             `bson:"status" json:"status"`                       // pending, in_progress, completed, failed, cancelled, partial
	Progress     int                `bson:"progress" json:"progress"`                   // 0-100
	Steps        []TransferStep     `bson:"steps" json:"steps"`
	Logs         []TransferLog      `bson:"logs" json:"logs"`
	Discovered   *DiscoveredData    `bson:"discovered,omitempty" json:"discovered,omitempty"`
	StartedAt    *time.Time         `bson:"started_at,omitempty" json:"started_at,omitempty"`
	CompletedAt  *time.Time         `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	CreatedAt    time.Time          `bson:"created_at" json:"created_at"`
}

// CreateTransferRequest is the request body to start a transfer.
//
// AuthMethod gates which credential field is required:
//
//   - "password" (or empty, for back-compat) → Password is required.
//   - "token" → Token is required; Password and Username are derived from
//     the redeem response on the source panel.
//
// PanelURL is optional and only consulted in token mode; it overrides the
// default `http://<source-ip>` redeem target so panels behind a custom
// nginx vhost (or HTTPS) can be reached without guessing.
type CreateTransferRequest struct {
	SourceIP   string             `json:"source_ip" validate:"required"`
	SourcePort int                `json:"source_port" validate:"required"`
	Username   string             `json:"username"`
	Password   string             `json:"password"`
	Protocol   string             `json:"protocol" validate:"required,oneof=ssh"`
	AuthMethod string             `json:"auth_method,omitempty"`
	Token      string             `json:"token,omitempty"`
	PanelURL   string             `json:"panel_url,omitempty"`
	Components TransferComponents `json:"components"`
	Selection  TransferSelection  `json:"selection"`
	Domains    []string           `json:"domains"`
}

// TestConnectionRequest is the request body to test remote connectivity.
//
// Like CreateTransferRequest, this accepts either a password or a transfer
// token. Password remains required when AuthMethod is empty so existing
// clients keep working.
type TestConnectionRequest struct {
	Protocol   string `json:"protocol" validate:"required,oneof=ftp sftp scp ssh"`
	Host       string `json:"host" validate:"required"`
	Port       int    `json:"port" validate:"required"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	AuthMethod string `json:"auth_method,omitempty"`
	Token      string `json:"token,omitempty"`
	PanelURL   string `json:"panel_url,omitempty"`
}

// DiscoverRequest is the request body to discover what exists on a source server.
type DiscoverRequest struct {
	SourceIP   string `json:"source_ip" validate:"required"`
	Port       int    `json:"port" validate:"required"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	AuthMethod string `json:"auth_method,omitempty"`
	Token      string `json:"token,omitempty"`
	PanelURL   string `json:"panel_url,omitempty"`
}
