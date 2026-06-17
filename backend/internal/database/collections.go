package database

const (
	ColUsers         = "users"
	ColDomains       = "domains"
	ColSubdomains    = "subdomains"
	ColAliases       = "domain_aliases"
	ColRedirects     = "domain_redirects"
	ColApps          = "apps"
	ColDeployments   = "deployments"
	ColDatabases     = "databases"
	ColDBUsers       = "database_users"
	ColMailboxes     = "mailboxes"
	ColForwarders    = "email_forwarders"
	ColAutoresponders = "email_autoresponders"
	ColDNSZones      = "dns_zones"
	ColDNSRecords    = "dns_records"
	ColSSLCerts      = "ssl_certificates"
	ColBackups       = "backups"
	ColBackupSchedules = "backup_schedules"
	ColWordPress     = "wordpress_installs"
	ColFirewallRules = "firewall_rules"
	ColBlockedIPs    = "blocked_ips"
	ColCronJobs      = "cron_jobs"
	ColCronHistory   = "cron_history"
	ColSSHKeys       = "ssh_keys"
	ColNotifications = "notifications"
	ColWebhooks      = "webhooks"
	ColAuditLogs     = "audit_logs"
	ColServerConfig  = "server_config"
	ColGitHubDeploys = "github_deploys"
	ColDeployKeys    = "deploy_api_keys"
	ColFTPAccounts          = "ftp_accounts"
	ColMetrics              = "metrics"
	ColEmailServerConfigs   = "email_server_configs"
	ColEmailInstallations   = "email_installations"
	ColPackages             = "hosting_packages"
	ColPackageRequests      = "package_change_requests"
	ColTransferJobs         = "transfer_jobs"
	ColTransferTokens       = "transfer_tokens"
	ColProjects             = "projects"
	ColProjectServices      = "project_services"
	ColProjectDeployments   = "project_deployments"
	ColDBAccessHosts        = "db_access_hosts"
	ColOTPRequests          = "otp_requests"
	ColLoginSessions        = "login_sessions"
	// ColBulkDeleteOTP holds the short-lived OTP requests gating
	// destructive bulk operations (initially: WHM Domains → Bulk
	// Delete). Separate from ColOTPRequests because the lifecycle is
	// different — these rows carry the target id list, expire fast,
	// and live in a flow that's NOT a login. Keeping them in their
	// own collection means a future "expire bulk-delete OTPs after
	// 5 minutes" cron doesn't have to filter ColOTPRequests rows.
	ColBulkDeleteOTP        = "bulk_delete_otp"
	// ColBulkMailboxOTP — same shape as ColBulkDeleteOTP but scoped
	// to the email/mailbox bulk operations: bulk delete (destructive)
	// and bulk export-with-password reveal (sensitive read). Kept in
	// its own collection so the schema can carry mailbox-shaped
	// fields (mailbox_ids, mailbox_addresses, action) without
	// polluting the domain bulk-delete row shape, AND so a future
	// retention sweep can cleanly target one or the other.
	ColBulkMailboxOTP       = "bulk_mailbox_otp"
	// ColBulkForwarderOTP — same lifecycle as ColBulkMailboxOTP but
	// for the email-forwarder bulk-delete flow added in v3.1.37.
	// Separate collection so a future retention sweep can target
	// each surface independently and so the row shape can carry
	// forwarder-specific fields (forwarder_ids, forwarder_sources)
	// without overloading the mailbox row.
	ColBulkForwarderOTP     = "bulk_forwarder_otp"
	// ColSSLBulkJobs — durable record of in-flight + completed bulk
	// SSL issuance runs (the "Issue / Reissue 27 certificates" flow).
	// One doc per operator click, holding the per-domain item list +
	// progress + current-domain pointer. Frontend polls this every
	// ~1.5s while issuing so the operator sees live progress instead
	// of staring at a "Reissuing 27..." spinner for 10 minutes.
	//
	// Why a dedicated collection (not ColProjectDeployments-style):
	// SSL bulk jobs don't carry the per-step stage timeline a deploy
	// does — there's no "clone / install / build" sequence, just one
	// certbot call per row. The row list is the source of truth; the
	// progress field is derived. Keeping it separate also means a
	// future retention sweep (e.g. drop SSL jobs older than 7 days)
	// doesn't touch deploy history.
	ColSSLBulkJobs        = "ssl_bulk_jobs"
	// API token + outbound webhook collections.
	// API tokens are GitHub-style: token id is public, secret is bcrypt-hashed.
	// Webhook endpoints are caller-owned URLs we POST to on subscribed events.
	// Deliveries are an attempt log, kept for ~30 days for debugging.
	ColAPITokens          = "api_tokens"
	ColWebhookEndpoints   = "webhook_endpoints"
	ColWebhookDeliveries  = "webhook_deliveries"

	// ColGuestLinks holds one-time, time-limited, browser-locked guest
	// links. A 3rd party mints one via the external API for a single
	// domain; opening the URL grants a no-login, hard-scoped session
	// (email + restricted DNS) that expires 30 minutes after first open
	// and refuses every browser but the first. Secret is bcrypt-hashed
	// at rest; the row carries the domain, link type, email limits, and
	// the first-open browser binding. See models/guest_link.go.
	ColGuestLinks         = "guest_links"

	// Mail-Suite admin: the panel records a registered mail-suite
	// deployment (URL + service token) per VPS / per vendor so the WHM
	// admin page can offer "Enable Mail" on a domain and open the
	// webmail. Mail-suite is a SEPARATE product (mail-suite/ subdir);
	// the existing /api/v1/cpanel/email/* and Roundcube SSO stay
	// untouched.
	ColMailSuiteDeployments = "mail_suite_deployments"
)
