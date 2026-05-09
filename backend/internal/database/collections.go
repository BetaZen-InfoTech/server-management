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
	// API token + outbound webhook collections.
	// API tokens are GitHub-style: token id is public, secret is bcrypt-hashed.
	// Webhook endpoints are caller-owned URLs we POST to on subscribed events.
	// Deliveries are an attempt log, kept for ~30 days for debugging.
	ColAPITokens          = "api_tokens"
	ColWebhookEndpoints   = "webhook_endpoints"
	ColWebhookDeliveries  = "webhook_deliveries"
)
