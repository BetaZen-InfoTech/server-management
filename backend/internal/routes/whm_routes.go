package routes

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/handlers"
	"github.com/betazeninfotech/whm-cpanel-management/internal/middleware"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/mongo"
)

type WHMHandlers struct {
	Domain       *handlers.DomainHandler
	App          *handlers.AppHandler
	Package      *handlers.PackageHandler
	Database     *handlers.DatabaseHandler
	Email        *handlers.EmailHandler
	DNS          *handlers.DNSHandler
	SSL          *handlers.SSLHandler
	Backup       *handlers.BackupHandler
	WordPress    *handlers.WordPressHandler
	Firewall     *handlers.FirewallHandler
	Software     *handlers.SoftwareHandler
	Monitoring   *handlers.MonitoringHandler
	Log          *handlers.LogHandler
	Cron         *handlers.CronHandler
	File         *handlers.FileHandler
	SSHKey       *handlers.SSHKeyHandler
	Process      *handlers.ProcessHandler
	Resource     *handlers.ResourceHandler
	Notification *handlers.NotificationHandler
	Audit        *handlers.AuditHandler
	Config       *handlers.ConfigHandler
	PanelMail    *handlers.PanelMailHandler
	Branding     *handlers.BrandingHandler
	HomePage     *handlers.HomePageHandler
	Maintenance  *handlers.MaintenanceHandler
	Deploy       *handlers.DeployHandler
	Project      *handlers.ProjectHandler
	User         *handlers.AuthHandler
	UserMgmt     *handlers.UserHandler
	Dashboard    *handlers.DashboardHandler
	Transfer     *handlers.TransferHandler
	TransferTok  *handlers.TransferTokenHandler
	Webhook      *handlers.WebhookHandler
	APIToken     *handlers.APITokenHandler
	WebhookEP    *handlers.WebhookEndpointHandler
	Programmatic *handlers.ProgrammaticHandler
	MailDiag     *handlers.MailDiagHandler
	MailSuite    *handlers.MailSuiteHandler
	MailLog      *handlers.MailLogHandler
	AuditService *services.AuditService
}

func RegisterWHMRoutes(app *fiber.App, cfg *config.Config, db *mongo.Database, h *WHMHandlers) {
	whm := app.Group("/api/v1/whm",
		middleware.Auth(cfg, db),
		middleware.InjectScope(),
		middleware.RequireRole("vendor_owner", "vendor_admin", "vendor_staff", "developer", "support"),
		middleware.RateLimiter(cfg.RateLimitWHM),
		middleware.AuditLogger(h.AuditService),
	)

	// Dashboard
	dashboard := whm.Group("/dashboard")
	dashboard.Get("/stats", h.Dashboard.WHMStats)
	dashboard.Get("/activity", h.Dashboard.WHMActivity)
	dashboard.Get("/server-status", h.Dashboard.WHMServerStatus)

	// Domains
	domains := whm.Group("/domains")
	domains.Get("/", middleware.RequirePermission("domain.view"), h.Domain.List)
	// Static /expiring route must live before /:id so Fiber's router
	// doesn't treat "expiring" as a domain id.
	domains.Get("/expiring", middleware.RequirePermission("domain.view"), h.Domain.ExpiringSoon)
	// Per-bucket counts for the dashboard's expiry filter pills.
	// Static path registered before /:id and before /expiring/:id
	// (Fiber matches literals first) so a domain whose id literally
	// reads "expiring" doesn't take this route.
	domains.Get("/expiring/buckets", middleware.RequirePermission("domain.view"), h.Domain.ExpiringBuckets)
	// WHOIS preview before a domain has been added. Used by the Add
	// Domain modal to auto-fill registrar + purchase + expiry dates
	// as soon as the operator finishes typing the name. Must come
	// BEFORE /:id for the same reason /expiring does.
	domains.Get("/whois", middleware.RequirePermission("domain.view"), h.Domain.WhoisLookupByName)
	// Domain preflight (WHOIS + DNS A / NS / MX + IP-match + firewall).
	// Static path — register BEFORE /:id so Fiber's router doesn't
	// match "preflight" as a domain id.
	domains.Post("/preflight", middleware.RequirePermission("domain.create"), h.Domain.Preflight)
	// Bulk upload — CSV/XLSX file with one row per domain. Both routes
	// must come BEFORE /:id so "bulk-upload" / "bulk-upload/template"
	// don't get parsed as a domain id.
	domains.Get("/bulk-upload/template", middleware.RequirePermission("domain.view"), h.Domain.BulkUploadTemplate)
	domains.Post("/bulk-upload", middleware.RequirePermission("domain.create"), h.Domain.BulkUpload)
	// Export selected (or all) domains as CSV / XLSX — same column shape
	// as the bulk-upload template, so export → edit in Excel → re-upload
	// is a round-trip without remapping.
	domains.Get("/export", middleware.RequirePermission("domain.view"), h.Domain.Export)
	// Bulk delete — WHM-owner-only, OTP-gated. Two-step flow: request
	// the code (mailed to the admin's address), confirm with token +
	// code (server runs Delete in a loop, returns per-row result).
	// vendor_owner gating prevents lower-privilege staff from triggering
	// destructive bulk operations even if `domain.manage` is granted.
	// Static paths registered BEFORE /:id so neither leg is parsed as
	// a domain id.
	domains.Post("/bulk-delete/request-otp",
		middleware.RequireRole("vendor_owner"), h.Domain.BulkDeleteRequestOTP)
	domains.Post("/bulk-delete/confirm",
		middleware.RequireRole("vendor_owner"), h.Domain.BulkDeleteConfirm)
	// Bulk WHOIS / RDAP refresh — non-destructive, no OTP gate. Body:
	// `{ ids?: string[], all?: bool }`. Tenant scope applies inside
	// the service so vendors only refresh their own domains.
	domains.Post("/whois-refresh-bulk", middleware.RequirePermission("domain.manage"), h.Domain.BulkRefreshRegistration)
	domains.Get("/:id", middleware.RequirePermission("domain.view"), h.Domain.Get)
	domains.Post("/", middleware.RequirePermission("domain.create"), h.Domain.Create)
	domains.Put("/:id", middleware.RequirePermission("domain.manage"), h.Domain.Update)
	domains.Patch("/:id/registration", middleware.RequirePermission("domain.manage"), h.Domain.UpdateRegistration)
	domains.Post("/:id/whois-refresh", middleware.RequirePermission("domain.manage"), h.Domain.WhoisLookup)
	// Re-verify an existing domain (re-runs preflight + stamps the
	// resolved fields back onto the doc).
	domains.Post("/:id/recheck", middleware.RequirePermission("domain.view"), h.Domain.Recheck)
	domains.Delete("/:id", middleware.RequirePermission("domain.delete"), h.Domain.Delete)
	domains.Patch("/:id/suspend", middleware.RequirePermission("domain.manage"), h.Domain.Suspend)
	domains.Patch("/:id/unsuspend", middleware.RequirePermission("domain.manage"), h.Domain.Unsuspend)
	domains.Patch("/:id/php", middleware.RequirePermission("domain.manage"), h.Domain.SwitchPHP)
	// 3.1.83 — change the nginx `root` directive for a domain in place.
	// Body: `{"document_root": "/absolute/path"}`. Empty = restore the
	// cPanel-default /home/<user>/domains/<domain>/public_html. Same
	// permission gate as SwitchPHP — it's a vhost-level edit only the
	// domain manager should touch.
	domains.Patch("/:id/document-root", middleware.RequirePermission("domain.manage"), h.Domain.SetDocumentRoot)
	domains.Get("/:id/stats", middleware.RequirePermission("domain.view"), h.Domain.Stats)

	// Packages
	packages := whm.Group("/packages")
	packages.Get("/", middleware.RequirePermission("package.view"), h.Package.List)
	// Vendor-facing plan switch — gated on package.view (everyone who
	// can see the catalog can submit a request). Static routes live
	// before /:id so Fiber's router matches the literal path first.
	packages.Post("/request-change", middleware.RequirePermission("package.view"), h.Package.RequestChange)
	packages.Get("/my-request", middleware.RequirePermission("package.view"), h.Package.MyRequest)
	packages.Post("/", middleware.RequirePermission("package.create"), h.Package.Create)
	packages.Get("/:id", middleware.RequirePermission("package.view"), h.Package.Get)
	packages.Put("/:id", middleware.RequirePermission("package.manage"), h.Package.Update)
	packages.Delete("/:id", middleware.RequirePermission("package.delete"), h.Package.Delete)
	// Admin review queue for plan-switch requests — platform owner only.
	pkgReq := whm.Group("/admin/package-requests", middleware.RequirePermission("server.manage"))
	pkgReq.Get("/", h.Package.ListRequests)
	pkgReq.Post("/:id/approve", h.Package.ApproveRequest)
	pkgReq.Post("/:id/reject", h.Package.RejectRequest)

	// Apps — specific routes must come before the generic /:name/:action catch-all.
	apps := whm.Group("/apps")
	apps.Get("/", middleware.RequirePermission("app.view"), h.App.List)
	apps.Get("/presets", middleware.RequirePermission("app.view"), h.App.Presets)
	apps.Post("/deploy", middleware.RequirePermission("app.deploy"), h.App.Deploy)
	apps.Get("/:name/logs", middleware.RequirePermission("app.view"), h.App.Logs)
	apps.Get("/:name/backups", middleware.RequirePermission("app.view"), h.App.ListBackups)
	apps.Post("/:name/backup", middleware.RequirePermission("app.manage"), h.App.Backup)
	apps.Post("/:name/restore", middleware.RequirePermission("app.manage"), h.App.Restore)
	apps.Post("/:name/transfer", middleware.RequirePermission("app.manage"), h.App.Transfer)
	apps.Post("/:name/redeploy", middleware.RequirePermission("app.deploy"), h.App.Redeploy)
	apps.Post("/:name/webhook/enable", middleware.RequirePermission("app.deploy"), h.Webhook.EnableWebhook)
	apps.Post("/:name/webhook/disable", middleware.RequirePermission("app.deploy"), h.Webhook.DisableWebhook)
	apps.Post("/:name/install-packages", middleware.RequirePermission("app.deploy"), h.App.InstallPackages)
	apps.Post("/:name/rollback", middleware.RequirePermission("app.deploy"), h.App.Rollback)
	apps.Put("/:name/env", middleware.RequirePermission("app.manage"), h.App.UpdateEnv)
	apps.Put("/:name", middleware.RequirePermission("app.manage"), h.App.Update)
	apps.Post("/:name/:action", middleware.RequirePermission("app.manage"), h.App.Action)
	apps.Delete("/:name", middleware.RequirePermission("app.manage"), h.App.Delete)
	apps.Get("/:name", middleware.RequirePermission("app.view"), h.App.Get)

	// Databases
	databases := whm.Group("/databases")
	databases.Get("/", middleware.RequirePermission("database.view"), h.Database.List)
	databases.Get("/:id", middleware.RequirePermission("database.view"), h.Database.Get)
	databases.Post("/", middleware.RequirePermission("database.create"), h.Database.Create)
	databases.Delete("/:id", middleware.RequirePermission("database.manage"), h.Database.Delete)
	databases.Get("/:id/connection", middleware.RequirePermission("database.view"), h.Database.GetConnection)
	databases.Get("/:id/phpmyadmin", middleware.RequirePermission("database.view"), h.Database.GetPhpMyAdmin)
	databases.Put("/:id/password", middleware.RequirePermission("database.manage"), h.Database.UpdateOwnerPassword)
	databases.Get("/:id/users", middleware.RequirePermission("database.view"), h.Database.ListUsers)
	databases.Post("/:id/users", middleware.RequirePermission("database.manage"), h.Database.CreateUser)
	databases.Put("/:id/users/:userId/password", middleware.RequirePermission("database.manage"), h.Database.UpdateUserPassword)
	databases.Put("/:id/users/:userId/role", middleware.RequirePermission("database.manage"), h.Database.UpdateUserRole)
	databases.Delete("/:id/users/:userId", middleware.RequirePermission("database.manage"), h.Database.DeleteUser)
	databases.Post("/:id/remote-access", middleware.RequirePermission("database.manage"), h.Database.EnableRemoteAccess)
	// cPanel-style per-database Remote Database Access — full CRUD on the
	// list of authorised hosts. The legacy /remote-access endpoint above
	// now delegates to these under the hood.
	databases.Get("/:id/access-hosts", middleware.RequirePermission("database.view"), h.Database.ListAccessHosts)
	databases.Post("/:id/access-hosts", middleware.RequirePermission("database.manage"), h.Database.AddAccessHost)
	databases.Delete("/:id/access-hosts/:hostId", middleware.RequirePermission("database.manage"), h.Database.RemoveAccessHost)

	// Email (static routes before parameterized to avoid /:id catching "forwarders" etc.)
	email := whm.Group("/email")
	email.Get("/", middleware.RequirePermission("email.view"), h.Email.ListMailboxes)
	email.Post("/", middleware.RequirePermission("email.create"), h.Email.CreateMailbox)
	// Mail log — structured, source-agnostic per-message log capturing
	// EVERY message Postfix touches: webmail, SMTP submission (587/465),
	// port-25 inbound, local sendmail/API (panel/PHP/cron/apps), and
	// third-party SMTP clients (Thunderbird/Outlook/mobile/external).
	// STATIC paths, registered before /:id so "logs" isn't parsed as a
	// mailbox id. The service applies tenant scoping from CallerScope.
	if h.MailLog != nil {
		email.Get("/logs", middleware.RequirePermission("email.view"), h.MailLog.List)
		email.Get("/logs/stats", middleware.RequirePermission("email.view"), h.MailLog.Stats)
	}
	email.Get("/forwarders", middleware.RequirePermission("email.view"), h.Email.ListForwarders)
	email.Post("/forwarders", middleware.RequirePermission("email.manage"), h.Email.CreateForwarder)
	// Forwarder bulk surface — STATIC paths BEFORE /:id so e.g.
	// "bulk-upload" isn't parsed as a forwarder id. Same OTP gate as
	// the mailbox bulk-delete; export needs no OTP because there's
	// no plaintext-secret column to reveal.
	email.Get("/forwarders/export", middleware.RequirePermission("email.view"), h.Email.ForwarderExport)
	email.Get("/forwarders/bulk-upload/template", middleware.RequirePermission("email.view"), h.Email.BulkForwarderUploadTemplate)
	email.Post("/forwarders/bulk-upload", middleware.RequirePermission("email.manage"), h.Email.BulkForwarderUpload)
	email.Post("/forwarders/bulk-delete/request-otp", middleware.RequireRole("vendor_owner"), h.Email.BulkForwarderDeleteRequestOTP)
	email.Post("/forwarders/bulk-delete/confirm", middleware.RequireRole("vendor_owner"), h.Email.BulkForwarderDeleteConfirm)
	// One-shot heal: rewrites /etc/postfix/virtual_alias_maps from
	// every Mongo forwarder row + reloads Postfix. Owner-only because
	// it touches Postfix config.
	email.Post("/forwarders/rehydrate", middleware.RequireRole("vendor_owner"), h.Email.RehydrateForwarderMaps)
	// One-shot mailbox rehydrate — rewrites /etc/dovecot/users +
	// /etc/postfix/virtual_mailbox_maps + virtual_mailbox_domains
	// from every Mongo mailbox row. Same code path the v3.1.47
	// server-transfer recovery calls; exposed here so an operator
	// who notices "transferred mailboxes don't deliver" can
	// reconcile from the panel without SSH. Owner-only because it
	// touches Dovecot + Postfix config.
	email.Post("/mailboxes/rehydrate", middleware.RequireRole("vendor_owner"), h.Email.RehydrateMailboxMaps)
	email.Delete("/forwarders/:id", middleware.RequirePermission("email.manage"), h.Email.DeleteForwarder)
	email.Put("/spam-settings/:domain", middleware.RequirePermission("email.manage"), h.Email.UpdateSpamSettings)
	email.Post("/dkim/:domain", middleware.RequirePermission("email.manage"), h.Email.SetupDKIM)
	email.Post("/webmail-token", middleware.RequirePermission("email.view"), h.Email.WebmailToken)
	// Server-wide reconcile — platform owner only (fixes Dovecot/Postfix
	// wiring on VPSes installed with the earlier fragile sed setup).
	email.Post("/reconcile-config", middleware.RequirePermission("server.manage"), h.Email.ReconcileConfig)
	// Bulk operations — static paths BEFORE /:id so neither leg gets
	// parsed as a mailbox id.
	//
	// Export: GET path with optional ?token=<otp>&code=<6-digit> for
	// password-reveal. Without the OTP, the column is dropped — same
	// permission gate (email.view) because the file is just directory
	// data the operator already sees in the table.
	email.Get("/export", middleware.RequirePermission("email.view"), h.Email.Export)
	email.Get("/bulk-upload/template", middleware.RequirePermission("email.view"), h.Email.BulkUploadTemplate)
	email.Post("/bulk-upload", middleware.RequirePermission("email.create"), h.Email.BulkUpload)
	// Bulk delete — vendor_owner only, OTP-gated. Two-step flow:
	// request the code (mailed to admin's address), confirm with
	// token + code (server runs DeleteMailbox in a loop). Mirrors
	// the WHM Domains bulk-delete safeguard.
	email.Post("/bulk-delete/request-otp",
		middleware.RequireRole("vendor_owner"), h.Email.BulkDeleteRequestOTP)
	email.Post("/bulk-delete/confirm",
		middleware.RequireRole("vendor_owner"), h.Email.BulkDeleteConfirm)
	// Bulk export password reveal — same OTP shape, kind=export. Step 2
	// is implicit: the GET /export endpoint above accepts the resulting
	// token + code as query params and decrypts AES password blobs.
	// Owner-gated because plaintext reveal is platform-sensitive.
	email.Post("/bulk-export/request-otp",
		middleware.RequireRole("vendor_owner"), h.Email.BulkExportRequestOTP)
	// Test-email per mailbox — static route BEFORE /:id so "test" isn't
	// parsed as a mailbox id.
	email.Post("/:id/test", middleware.RequirePermission("email.manage"), h.Email.SendTest)
	email.Get("/:id", middleware.RequirePermission("email.view"), h.Email.GetMailbox)
	email.Put("/:id", middleware.RequirePermission("email.manage"), h.Email.UpdateMailbox)
	email.Delete("/:id", middleware.RequirePermission("email.manage"), h.Email.DeleteMailbox)

	// DNS
	dns := whm.Group("/dns")
	// Bulk TTL update — sweeps every zone the caller can see, retunes
	// any record whose type is in the requested set, reconciles each
	// affected rrset to PowerDNS. Tenant scoping is enforced inside
	// the service via CallerScope; no extra middleware needed here.
	// Registered BEFORE /zones/* so Fiber doesn't try to match
	// "bulk-ttl" against the :domain wildcard.
	dns.Post("/bulk-ttl", middleware.RequirePermission("dns.manage"), h.DNS.BulkUpdateTTL)
	dns.Get("/zones", middleware.RequirePermission("dns.view"), h.DNS.ListZones)
	dns.Get("/zones/:domain", middleware.RequirePermission("dns.view"), h.DNS.GetZone)
	dns.Post("/zones", middleware.RequirePermission("dns.manage"), h.DNS.CreateZone)
	dns.Delete("/zones/:domain", middleware.RequirePermission("dns.manage"), h.DNS.DeleteZone)
	dns.Get("/zones/:domain/records", middleware.RequirePermission("dns.view"), h.DNS.ListRecords)
	dns.Post("/zones/:domain/records", middleware.RequirePermission("dns.manage"), h.DNS.AddRecord)
	// Bulk-add: register BEFORE the /:id routes so Fiber doesn't match
	// "bulk" as an ObjectID. Used by the inline-edit "Save All Records"
	// button to commit N pending rows in one round-trip.
	dns.Post("/zones/:domain/records/bulk", middleware.RequirePermission("dns.manage"), h.DNS.BulkAddRecords)
	dns.Put("/zones/:domain/records/:id", middleware.RequirePermission("dns.manage"), h.DNS.UpdateRecord)
	dns.Delete("/zones/:domain/records/:id", middleware.RequirePermission("dns.manage"), h.DNS.DeleteRecord)
	dns.Get("/zones/:domain/export", middleware.RequirePermission("dns.view"), h.DNS.ExportZone)
	// Heal a zone whose PowerDNS rrsets no longer match the Mongo
	// records collection — collapses duplicate Mongo rows and rewrites
	// every rrset via replace-rrset. Idempotent. Gated on dns.manage
	// because it mutates PowerDNS.
	dns.Post("/zones/:domain/reconcile", middleware.RequirePermission("dns.manage"), h.DNS.ReconcileZone)

	// SSL
	ssl := whm.Group("/ssl", middleware.RequirePermission("ssl.manage"))
	ssl.Get("/", h.SSL.List)
	ssl.Get("/:domain", h.SSL.Get)
	ssl.Post("/letsencrypt", h.SSL.IssueLetsEncrypt)
	// Bulk issue must be registered BEFORE /:domain so Fiber's router
	// doesn't treat "letsencrypt/bulk" as a {domain} param.
	ssl.Post("/letsencrypt/bulk", h.SSL.IssueLetsEncryptBulk)
	// Bulk-issue job polling + cancel (v3.1.60). The bulk POST above
	// now returns a job_id immediately and the frontend polls these
	// endpoints for live progress. Same /:id ordering rule applies —
	// the literal segment `bulk-jobs` comes before any /:domain
	// pattern below.
	ssl.Get("/bulk-jobs/:id", h.SSL.BulkIssueJobStatus)
	ssl.Post("/bulk-jobs/:id/cancel", h.SSL.BulkIssueJobCancel)
	// Bulk Force-HTTPS (HTTPS-only redirect) on every selected /
	// every visible domain. Body: { ids?: string[], all?: bool,
	// enable: bool }. Domains without a live cert are SKIPPED so we
	// don't 502 an HTTP-only site by accident.
	ssl.Post("/force-ssl-bulk", h.SSL.BulkForceSSL)
	ssl.Post("/custom", h.SSL.UploadCustom)
	ssl.Post("/:domain/renew", h.SSL.Renew)
	// Reissue forces a fresh cert NOW for an existing domain — same
	// downstream pipeline as a first issue (vhost upgrade, mail-SSL
	// retrigger, ssl.issued webhook). Distinct from renew which uses
	// `certbot renew` and only works when the live cert is on disk.
	ssl.Post("/:domain/reissue", h.SSL.Reissue)
	ssl.Post("/:domain/revoke", h.SSL.Revoke)
	ssl.Post("/:domain/force-ssl", h.SSL.ForceSSL)
	ssl.Delete("/:domain", h.SSL.Delete)

	// Backups (static routes before parameterized to avoid /:id catching "schedules" etc.)
	backups := whm.Group("/backups")
	backups.Get("/", middleware.RequirePermission("backup.view"), h.Backup.List)
	backups.Post("/", middleware.RequirePermission("backup.create"), h.Backup.Create)
	backups.Post("/restore", middleware.RequirePermission("backup.restore"), h.Backup.Restore)
	backups.Post("/restore/upload", middleware.RequirePermission("backup.restore"), h.Backup.RestoreUpload)
	backups.Post("/test-connection", middleware.RequirePermission("backup.create"), h.Backup.TestConnection)
	backups.Get("/schedules", middleware.RequirePermission("backup.view"), h.Backup.ListSchedules)
	backups.Post("/schedules", middleware.RequirePermission("backup.schedule"), h.Backup.CreateSchedule)
	backups.Delete("/schedules/:id", middleware.RequirePermission("backup.schedule"), h.Backup.DeleteSchedule)
	backups.Get("/:id", middleware.RequirePermission("backup.view"), h.Backup.Get)
	backups.Delete("/:id", middleware.RequirePermission("backup.delete"), h.Backup.Delete)
	backups.Get("/:id/download", middleware.RequirePermission("backup.view"), h.Backup.Download)

	// WordPress
	wp := whm.Group("/wordpress", middleware.RequirePermission("wordpress.manage"))
	wp.Get("/", h.WordPress.List)
	wp.Get("/check-conflict", h.WordPress.CheckConflict)
	wp.Post("/install", h.WordPress.Install)
	wp.Post("/rescan", h.WordPress.Rescan)
	wp.Get("/:id", h.WordPress.Get)
	wp.Delete("/:id", h.WordPress.Delete)
	wp.Post("/:id/update", h.WordPress.Update)
	wp.Post("/:id/security-scan", h.WordPress.SecurityScan)
	wp.Get("/:id/plugins", h.WordPress.ListPlugins)
	wp.Post("/:id/plugins", h.WordPress.InstallPlugin)
	// Bulk plugin update (no slug). Registered before the /:slug routes
	// so Fiber doesn't try to match "update" as a slug value.
	wp.Post("/:id/plugins/update", h.WordPress.UpdatePlugin)
	wp.Post("/:id/plugins/:slug/activate", h.WordPress.ActivatePlugin)
	wp.Post("/:id/plugins/:slug/deactivate", h.WordPress.DeactivatePlugin)
	wp.Post("/:id/plugins/:slug/update", h.WordPress.UpdatePlugin)
	wp.Delete("/:id/plugins/:slug", h.WordPress.DeletePlugin)
	// Themes — same shape as plugins.
	wp.Get("/:id/themes", h.WordPress.ListThemes)
	wp.Post("/:id/themes", h.WordPress.InstallTheme)
	wp.Post("/:id/themes/update", h.WordPress.UpdateTheme)
	wp.Post("/:id/themes/:slug/activate", h.WordPress.ActivateTheme)
	wp.Post("/:id/themes/:slug/update", h.WordPress.UpdateTheme)
	wp.Delete("/:id/themes/:slug", h.WordPress.DeleteTheme)
	// Detach — drop DB row, preserve files + database on disk.
	wp.Post("/:id/detach", h.WordPress.Detach)
	wp.Patch("/:id/maintenance", h.WordPress.ToggleMaintenance)
	wp.Patch("/:id/auto-update", h.WordPress.ToggleAutoUpdate)
	wp.Post("/:id/auto-login", h.WordPress.AutoLogin)
	wp.Get("/:id/users", h.WordPress.ListUsers)
	wp.Post("/:id/users", h.WordPress.CreateUser)
	wp.Delete("/:id/users/:uid", h.WordPress.DeleteUser)
	wp.Patch("/:id/users/:uid", h.WordPress.UpdateUserRole)

	// Firewall
	fw := whm.Group("/firewall", middleware.RequirePermission("firewall.manage"))
	fw.Get("/status", h.Firewall.Status)
	fw.Get("/rules", h.Firewall.ListRules)
	fw.Post("/allow", h.Firewall.AllowPort)
	fw.Post("/deny", h.Firewall.DenyPort)
	fw.Delete("/rules/:id", h.Firewall.DeleteRule)
	fw.Post("/block-ip", h.Firewall.BlockIP)
	fw.Post("/unblock-ip", h.Firewall.UnblockIP)
	fw.Get("/blocked-ips", h.Firewall.ListBlockedIPs)
	fw.Get("/fail2ban/status", h.Firewall.Fail2BanStatus)
	fw.Put("/fail2ban/config", h.Firewall.UpdateFail2Ban)

	// Software
	// /software is a server-level catalog (PHP / Node / Python / runtime
	// installs). Vendors don't install or remove runtimes — gate the
	// whole group on server.manage so URL-typing can't bypass the
	// sidebar hide.
	sw := whm.Group("/software", middleware.RequirePermission("server.manage"))
	sw.Get("/installed", h.Software.ListInstalled)
	sw.Get("/packages", h.Software.ListInstalled)
	sw.Post("/install", h.Software.Install)
	sw.Post("/uninstall", h.Software.Uninstall)
	sw.Get("/updates", h.Software.CheckUpdates)
	sw.Post("/install-email", h.Software.InstallEmail)
	sw.Get("/email-status", h.Software.EmailStatus)
	sw.Get("/email-installation/:id", h.Software.GetEmailInstallation)
	sw.Put("/email-settings", h.Software.UpdateEmailSettings)
	// Runtime version management
	sw.Get("/runtimes", h.Software.ListAllRuntimes)
	// Literal sub-paths MUST register before the `:runtime` wildcard,
	// otherwise Fiber matches /runtimes/defaults as runtime=defaults
	// and ListRuntimeVersions errors with "unsupported runtime: defaults".
	sw.Post("/runtimes/install", h.Software.InstallRuntime)
	sw.Post("/runtimes/uninstall", h.Software.UninstallRuntime)
	// Per-runtime default version. Apps/services with empty
	// runtime_version pick this up at deploy time so the operator
	// can flip the host-wide active Node/Go/Ruby/Python with one
	// click instead of editing every app individually.
	sw.Get("/runtimes/defaults", h.Software.GetRuntimeDefaults)
	sw.Post("/runtimes/defaults", h.Software.SetRuntimeDefault)
	sw.Get("/runtimes/:runtime", h.Software.ListRuntimeVersions)
	// PHP extensions
	sw.Get("/php/:version/extensions", h.Software.ListPHPExtensions)
	sw.Post("/php/:version/extensions/install", h.Software.InstallPHPExtension)
	sw.Post("/php/:version/extensions/uninstall", h.Software.UninstallPHPExtension)
	// PHP-FPM management
	sw.Get("/php/:version/fpm/pools", h.Software.ListPHPFPMPools)
	sw.Get("/php/:version/fpm/status", h.Software.GetPHPFPMStatus)
	sw.Post("/php/:version/fpm/restart", h.Software.RestartPHPFPM)
	sw.Post("/php/:version/fpm/pools/:pool/enable", h.Software.EnablePHPFPMPool)
	sw.Post("/php/:version/fpm/pools/:pool/disable", h.Software.DisablePHPFPMPool)

	// Monitoring
	monitor := whm.Group("/monitor", middleware.RequirePermission("monitor.view"))
	monitor.Get("/system", h.Monitoring.SystemInfo)
	monitor.Get("/metrics", h.Monitoring.Metrics)
	monitor.Get("/services", h.Monitoring.Services)
	monitor.Get("/history", h.Monitoring.History)
	monitor.Get("/alerts", h.Monitoring.GetAlerts)
	monitor.Put("/alerts", h.Monitoring.UpdateAlerts)
	// WHM-style Server Information + Service Status pages. Gated on
	// server.view on top of monitor.view because they exec a few extra
	// shell probes (free / df / dmesg) the lighter /system endpoint
	// doesn't — same permission guarding the per-tenant usage views.
	monitor.Get("/server-info", middleware.RequirePermission("server.view"), h.Monitoring.ServerInformation)
	monitor.Get("/service-status", middleware.RequirePermission("server.view"), h.Monitoring.ServiceStatusSummary)

	// Logs
	logs := whm.Group("/logs", middleware.RequirePermission("log.view"))
	logs.Get("/files", h.Log.ListFiles)
	logs.Get("/:type", h.Log.View)
	logs.Get("/:type/search", h.Log.Search)
	logs.Get("/:type/download", h.Log.Download)

	// Cron Jobs
	cron := whm.Group("/cron", middleware.RequirePermission("cron.manage"))
	cron.Get("/", h.Cron.List)
	cron.Get("/:id", h.Cron.Get)
	cron.Post("/", h.Cron.Create)
	cron.Put("/:id", h.Cron.Update)
	cron.Delete("/:id", h.Cron.Delete)
	cron.Patch("/:id/toggle", h.Cron.Toggle)
	cron.Post("/:id/run", h.Cron.RunNow)
	cron.Get("/:id/history", h.Cron.History)

	// Files
	files := whm.Group("/files", middleware.RequirePermission("file.manage"))
	files.Get("/list", h.File.ListDir)
	files.Get("/read", h.File.ReadFile)
	files.Post("/create", h.File.CreateFile)
	files.Post("/upload", h.File.Upload)
	files.Put("/edit", h.File.EditFile)
	files.Delete("/delete", h.File.DeleteFile)
	// Trash — soft-delete inbox. Delete with permanent=false lands
	// files here; these endpoints let the UI list, restore, and empty
	// the per-user trash.
	files.Get("/trash", h.File.ListTrash)
	files.Post("/trash/restore", h.File.RestoreTrash)
	files.Delete("/trash", h.File.DeleteTrash)
	files.Post("/rename", h.File.Rename)
	files.Post("/chmod", h.File.Chmod)
	files.Post("/compress", h.File.Compress)
	files.Post("/extract", h.File.Extract)
	files.Post("/copy", h.File.Copy)
	files.Post("/move", h.File.Move)
	files.Get("/search", h.File.Search)
	files.Get("/download", h.File.Download)
	files.Get("/info", h.File.Info)
	files.Post("/protect", h.File.PasswordProtect)
	files.Post("/unprotect", h.File.Unprotect)

	// SSH Keys
	sshKeys := whm.Group("/ssh-keys", middleware.RequirePermission("ssh.manage"))
	sshKeys.Get("/:user", h.SSHKey.List)
	sshKeys.Post("/:user", h.SSHKey.Add)
	sshKeys.Delete("/:user/:id", h.SSHKey.Delete)
	sshKeys.Post("/:user/generate", h.SSHKey.Generate)

	// Processes (static routes before parameterized to avoid /:pid catching "services")
	// /processes inspects + kills server-wide processes. Owner only.
	procs := whm.Group("/processes", middleware.RequirePermission("server.manage"))
	procs.Get("/", middleware.RequirePermission("process.view"), h.Process.List)
	procs.Get("/services", middleware.RequirePermission("process.view"), h.Process.ListServices)
	procs.Post("/services/:name/:action", middleware.RequirePermission("process.manage"), h.Process.ControlService)
	procs.Get("/:pid", middleware.RequirePermission("process.view"), h.Process.Get)
	procs.Post("/:pid/kill", middleware.RequirePermission("process.manage"), h.Process.Kill)

	// Resources
	// /resources reports server-wide disk + bandwidth. Vendors get
	// their own per-tenant view in the user panel — they don't see
	// platform-wide aggregates here.
	res := whm.Group("/resources", middleware.RequirePermission("server.manage"))
	res.Get("/summary", middleware.RequirePermission("server.view"), h.Resource.Summary)
	res.Get("/domains/:domain", middleware.RequirePermission("domain.view"), h.Resource.DomainUsage)
	res.Get("/bandwidth", middleware.RequirePermission("server.view"), h.Resource.Bandwidth)
	res.Get("/bandwidth/:domain", middleware.RequirePermission("domain.view"), h.Resource.BandwidthByDomain)
	// Reports — top IPs / URLs / per-domain. Parses nginx access logs;
	// `?domain=` optional (empty = whole server). Read-only, gated on
	// server.view to keep the bar low — this is informational only.
	res.Get("/traffic-stats", middleware.RequirePermission("server.view"), h.Resource.TrafficStats)
	res.Put("/domains/:domain/limits", middleware.RequirePermission("domain.manage"), h.Resource.UpdateLimits)
	// Vendors rollup + per-vendor breakdown — gated on server.manage so
	// only the platform owner sees the cross-tenant aggregation. Tenant
	// vendors get their own view via the existing per-domain endpoints.
	res.Get("/vendors", middleware.RequirePermission("server.manage"), h.Resource.Vendors)
	res.Get("/vendors/:id", middleware.RequirePermission("server.manage"), h.Resource.VendorDetail)

	// Diagnostics — platform-owner-only mail-stack health page.
	// GET returns the structured DiagnosticReport (every check
	// + status + ProblemType + Symptom + Resolution playbook).
	// POST runs the per-check FixCommand for each id in the body
	// (apt install / systemctl start / postmap — never `rm -rf`).
	diag := whm.Group("/diagnostics", middleware.RequirePermission("server.manage"))
	diag.Get("/mail-stack", h.MailDiag.Diagnose)
	diag.Post("/mail-stack/fix", h.MailDiag.AutoFix)

	// Notifications
	notif := whm.Group("/notifications", middleware.RequirePermission("notification.manage"))
	notif.Get("/settings", h.Notification.GetSettings)
	notif.Put("/settings", h.Notification.UpdateSettings)
	notif.Get("/history", h.Notification.History)

	// Webhooks
	webhooks := whm.Group("/webhooks", middleware.RequirePermission("server.manage"))
	webhooks.Get("/", h.Notification.ListWebhooks)
	webhooks.Post("/", h.Notification.CreateWebhook)
	webhooks.Delete("/:id", h.Notification.DeleteWebhook)
	webhooks.Post("/:id/test", h.Notification.TestWebhook)

	// Audit
	audit := whm.Group("/audit")
	audit.Get("/", middleware.RequirePermission("audit.view"), h.Audit.List)
	audit.Get("/export", middleware.RequirePermission("server.manage"), h.Audit.Export)
	audit.Get("/:id", middleware.RequirePermission("audit.view"), h.Audit.Get)

	// Config
	serverCfg := whm.Group("/config", middleware.RequirePermission("config.manage"))
	serverCfg.Get("/", h.Config.Get)
	serverCfg.Put("/nginx", h.Config.UpdateNginx)
	serverCfg.Put("/php/:version", h.Config.UpdatePHP)
	serverCfg.Put("/mongodb", h.Config.UpdateMongoDB)
	serverCfg.Put("/hostname", h.Config.UpdateHostname)
	serverCfg.Put("/timezone", h.Config.UpdateTimezone)
	serverCfg.Put("/contact-email", h.Config.UpdateContactEmail)
	serverCfg.Post("/nginx/test", h.Config.TestNginx)
	serverCfg.Get("/panel-domain", h.Config.GetPanelDomain)
	serverCfg.Put("/panel-domain", h.Config.UpdatePanelDomain)
	// UI feature flags — toggles for the demo-credentials blocks on the
	// login pages and the sample-values hint on the transfer wizard. The
	// GET mirror of /api/v1/public-settings, but for the authenticated
	// admin UI it's grouped with the rest of the panel config.
	serverCfg.Get("/ui-settings", h.Config.GetUISettings)
	serverCfg.Put("/ui-settings", middleware.RequirePermission("server.manage"), h.Config.UpdateUISettings)
	// Panel SSL — install/renew a Let's Encrypt cert for the current
	// panel domain, decoupled from domain change so an operator who
	// skipped auto-SSL during the connect step can flip it on later.
	serverCfg.Get("/panel-ssl", h.Config.GetPanelSSL)
	serverCfg.Post("/panel-ssl", h.Config.InstallPanelSSL)
	// Whole-server IP migration — rewrites all A / SPF / domain / env /
	// panel-vhost references from old_ip to new_ip in one shot.
	serverCfg.Post("/reassign-ip", h.Config.ReassignIP)
	// Outgoing-mail (SMTP) config — password-reset emails, notifications.
	// Gated on server.manage; SMTP password is AES-GCM encrypted at rest
	// and never echoed back in GET responses (only `has_password: true`).
	serverCfg.Get("/mail", middleware.RequirePermission("server.manage"), h.PanelMail.Get)
	serverCfg.Put("/mail", middleware.RequirePermission("server.manage"), h.PanelMail.Save)
	serverCfg.Post("/mail/test", middleware.RequirePermission("server.manage"), h.PanelMail.Test)
	// Branding (panel name + logo + favicon). Stored as data: URLs in the
	// `branding` server_config singleton; reads also exposed unauth at
	// /api/v1/branding so index.html / login pages can render the
	// configured chrome before any token exists.
	serverCfg.Get("/branding", middleware.RequirePermission("server.manage"), h.Branding.Get)
	serverCfg.Put("/branding", middleware.RequirePermission("server.manage"), h.Branding.Save)
	// Home page — public landing at GET / (rendered server-side). Same
	// shape as Branding: singleton in server_config (`_id: home_page`),
	// admin read+write here, public read at /api/v1/home-page.
	serverCfg.Get("/home-page", middleware.RequirePermission("server.manage"), h.HomePage.Get)
	serverCfg.Put("/home-page", middleware.RequirePermission("server.manage"), h.HomePage.Save)
	// WHM-style Edit Database Configuration (my.cnf) + Repair Databases.
	// Both gated on server.manage — they restart mariadb and can impact
	// every tenant on the box.
	serverCfg.Get("/mysql", middleware.RequirePermission("server.manage"), h.Config.GetMySQLConfig)
	serverCfg.Put("/mysql", middleware.RequirePermission("server.manage"), h.Config.UpdateMySQLConfig)
	serverCfg.Get("/mysql/databases", middleware.RequirePermission("server.manage"), h.Config.ListMySQLDatabases)
	serverCfg.Post("/mysql/repair", middleware.RequirePermission("server.manage"), h.Config.RepairDatabase)
	// WHM MongoDB admin surface (v3.1.109) — server-level Mongo administration
	// (config, auth on/off, raw mongod.conf, all-DB + super-admin user view).
	// Reads stay at the group's config.manage; every MUTATING op adds
	// server.manage because it restarts mongod (which the panel itself uses)
	// — same bar as the MySQL config + Reboot above. PUT /mongodb already
	// exists above; these add the GET + raw + user/db surfaces. The mutating
	// mongod.conf writes go through applyMongodChangeSafely (auto-rollback).
	serverCfg.Get("/mongodb", h.Config.GetMongoDBConfig)
	serverCfg.Get("/mongodb/status", h.Config.GetMongoStatus)
	serverCfg.Get("/mongodb/databases", h.Config.ListMongoDatabases)
	serverCfg.Get("/mongodb/raw", middleware.RequirePermission("server.manage"), h.Config.GetMongoRawConf)
	serverCfg.Put("/mongodb/raw", middleware.RequirePermission("server.manage"), h.Config.UpdateMongoRawConf)
	serverCfg.Get("/mongodb/users", h.Config.ListMongoAdminUsers)
	serverCfg.Post("/mongodb/users", middleware.RequirePermission("server.manage"), h.Config.CreateMongoAdminUser)
	serverCfg.Delete("/mongodb/users/:username", middleware.RequirePermission("server.manage"), h.Config.DeleteMongoAdminUser)
	// MultiPHP INI Editor — per-version php.ini basic-mode editor.
	serverCfg.Get("/php/versions", middleware.RequirePermission("server.manage"), h.Config.ListPHPVersions)
	serverCfg.Get("/php/:version/ini", middleware.RequirePermission("server.manage"), h.Config.GetPHPIni)
	serverCfg.Put("/php/:version/ini", middleware.RequirePermission("server.manage"), h.Config.UpdatePHPIni)
	// Reboot — shutdown -r +1 (graceful) and `systemctl --force reboot`.
	serverCfg.Post("/reboot/graceful", middleware.RequirePermission("server.manage"), h.Config.GracefulReboot)
	serverCfg.Post("/reboot/forceful", middleware.RequirePermission("server.manage"), h.Config.ForcefulReboot)
	serverCfg.Post("/:service/restart", h.Config.RestartService)

	// Maintenance
	// /maintenance toggles platform-wide maintenance mode + restarts
	// shared services (nginx / php-fpm / mongo). Owner only.
	maint := whm.Group("/maintenance", middleware.RequirePermission("server.manage"))
	maint.Get("/", middleware.RequirePermission("server.view"), h.Maintenance.Status)
	maint.Post("/enable", middleware.RequirePermission("server.manage"), h.Maintenance.EnableServer)
	maint.Post("/disable", middleware.RequirePermission("server.manage"), h.Maintenance.DisableServer)
	maint.Post("/domains/:domain/enable", middleware.RequirePermission("domain.manage"), h.Maintenance.EnableDomain)
	maint.Post("/domains/:domain/disable", middleware.RequirePermission("domain.manage"), h.Maintenance.DisableDomain)

	// Deploy Software (Projects) — replaces the legacy /deploy page. Every
	// route is gated on the same `deploy.manage` permission that guarded the
	// old page, so existing roles keep working without a migration.
	projects := whm.Group("/projects", middleware.RequirePermission("deploy.manage"))
	projects.Get("/", h.Project.List)
	// Flat inventory: every Deploy Software service the caller can
	// see, paginated, with project name/slug stamped on each row.
	// Static path registered BEFORE /:id so Fiber doesn't match
	// "services" as a project id.
	projects.Get("/services", h.Project.ListAllServices)
	projects.Post("/", h.Project.Create)
	projects.Post("/provision", h.Project.Provision)
	// JSON import — a literal path registered BEFORE the parameterised
	// /:id routes so Fiber doesn't try to match "import" as a project id
	// on a POST. Same routing-precedence pattern as /services and
	// /provision above.
	projects.Post("/import", h.Project.Import)
	// 3.1.91 — panel-wide rolling restart. Literal path BEFORE /:id
	// for the same Fiber-precedence reason. Extra gate on
	// `server.manage` so vendors with `deploy.manage` can't trigger
	// a restart across other tenants' projects — platform-owner
	// scope only.
	projects.Post("/restart-rolling-all",
		middleware.RequirePermission("server.manage"),
		h.Project.RestartAllProjectsRolling)
	projects.Get("/:id", h.Project.Get)
	projects.Put("/:id", h.Project.Update)
	projects.Delete("/:id", h.Project.Delete)
	projects.Post("/:id/deploy", h.Project.DeployAll)
	projects.Post("/:id/rotate-pat", h.Project.RotatePAT)
	projects.Post("/:id/regenerate-webhook-secret", h.Project.RegenerateWebhookSecret)
	projects.Post("/:id/pause", h.Project.Pause)
	projects.Post("/:id/resume", h.Project.Resume)
	// Project-wide start/stop/restart across every backend service in the
	// project. action ∈ {start, stop, restart}. Registered before the more
	// specific /services/:svc/... routes to avoid Fiber's prefix-first
	// matching treating "action" as a service id.
	projects.Post("/:id/action/:action", h.Project.ProjectAction)
	// 3.1.90 — Rolling restart: restart every backend service one at a
	// time, waiting for each to become systemctl `active` before
	// moving on. Stops on first service that fails to come back so a
	// broken process doesn't knock the rest down. Same gating as the
	// generic action endpoint.
	projects.Post("/:id/restart-rolling", h.Project.RollingRestart)
	projects.Get("/:id/webhook", h.Project.WebhookInfo)
	// JSON export — emits a Content-Disposition: attachment response so
	// the browser saves the manifest directly without rendering the
	// env-vars-bearing payload on screen. Reused by the Import flow.
	projects.Get("/:id/export", h.Project.Export)
	projects.Get("/:id/activity", h.Project.Activity)
	projects.Get("/:id/services", h.Project.ListServices)
	projects.Post("/:id/services", h.Project.AddService)
	// Bulk add via CSV / XLSX upload. Same AddService pipeline per row,
	// so framework presets / port allocation / vhost + SSL all behave
	// identically to the single-create flow. Template GET registered
	// BEFORE the parameterised /:svc routes so Fiber matches the
	// literal `bulk/template` path instead of treating "bulk" as a
	// service id.
	projects.Get("/:id/services/bulk/template", h.Project.BulkAddServicesTemplate)
	projects.Post("/:id/services/bulk", h.Project.BulkAddServices)
	// JSON variants of the bulk surface — three new operations on the
	// Services toolbar:
	//
	//   GET    /:id/services/export       — download all services as a
	//                                       portable JSON manifest
	//   POST   /:id/services/import-json  — additive: add new services
	//                                       from a JSON manifest body
	//   PUT    /:id/services/bulk-edit    — in-place: update existing
	//                                       services from a JSON body
	//
	// All three registered BEFORE the parameterised /:svc routes so
	// Fiber's literal-first matcher doesn't read "export" / "import-
	// json" / "bulk-edit" as service IDs.
	projects.Get("/:id/services/export", h.Project.ExportServices)
	projects.Post("/:id/services/import-json", h.Project.BulkAddServicesJSON)
	projects.Put("/:id/services/bulk-edit", h.Project.BulkEditServicesJSON)
	projects.Put("/:id/services/:svc", h.Project.UpdateService)
	projects.Delete("/:id/services/:svc", h.Project.RemoveService)
	projects.Post("/:id/services/:svc/deploy", h.Project.DeployService)
	projects.Get("/:id/services/:svc/deployments/latest", h.Project.LatestDeployment)
	// Per-service start/stop/restart (systemctl, no rebuild).
	projects.Post("/:id/services/:svc/action/:action", h.Project.ServiceAction)
	projects.Get("/:id/services/:svc/logs", h.Project.Logs)
	projects.Post("/:id/services/:svc/aliases", h.Project.AddAlias)
	projects.Delete("/:id/services/:svc/aliases/:domain", h.Project.RemoveAlias)

	// Users — specific routes registered before the parameterised /:id ones.
	// Gated on user.create so vendor_admin can manage their own tenant team.
	// Tenant isolation is enforced inside the service layer.
	users := whm.Group("/users", middleware.RequirePermission("user.create"))
	users.Get("/", h.UserMgmt.List)
	users.Post("/", h.UserMgmt.Create)
	// Manage Shell Access — static paths before the parameterised
	// /:id ones so Fiber's router matches the literal strings first.
	users.Get("/shell-access", h.UserMgmt.ListShellAccess)
	users.Post("/:id/shell-access", h.UserMgmt.SetShellAccess)
	users.Post("/:id/suspend", h.UserMgmt.Suspend)
	users.Post("/:id/activate", h.UserMgmt.Activate)
	users.Post("/:id/reset-password", h.UserMgmt.ResetPassword)
	users.Get("/:id", h.UserMgmt.Get)
	users.Put("/:id", h.UserMgmt.Update)
	users.Delete("/:id", h.UserMgmt.Delete)

	// Limit Bandwidth on an Account — uses the domain id, not the user
	// id, because bandwidth caps in this panel are per-domain. Gated on
	// server.manage because it writes to every tenant's domains.
	bw := whm.Group("/bandwidth-limits", middleware.RequirePermission("server.manage"))
	bw.Get("/", h.UserMgmt.ListBandwidthLimits)
	bw.Put("/:id", h.UserMgmt.SetBandwidthLimit)

	// Vendors — platform-owner only. Lists / inspects tenant root accounts
	// (vendor_admin role). Tenant-scoped users use /users for their own team.
	vendors := whm.Group("/admin/vendors", middleware.RequirePermission("server.manage"))
	vendors.Get("/", h.UserMgmt.AdminListVendors)
	vendors.Get("/stats", h.UserMgmt.AdminVendorStats)
	// Static /trash route must live BEFORE /:id/* so Fiber's router
	// matches the literal path instead of treating "trash" as an id.
	vendors.Get("/trash", h.UserMgmt.AdminListTrashedVendors)
	vendors.Post("/:id/trash", h.UserMgmt.AdminTrashVendor)
	vendors.Post("/:id/restore", h.UserMgmt.AdminRestoreVendor)
	// Permanent-delete from the Trash tab — tears down every domain /
	// mail / DNS / FTP record and renames the linux user to
	// <username>-deleted-<ts>, preserving files under the new home.
	vendors.Delete("/:id/purge", h.UserMgmt.AdminPurgeVendor)
	vendors.Put("/:id/package", h.UserMgmt.AdminUpdateVendorPackage)
	vendors.Get("/:id/storage", h.UserMgmt.AdminVendorStorage)
	vendors.Post("/:id/impersonate", h.UserMgmt.AdminImpersonateVendor)

	// Transfers (static routes before parameterized)
	// /transfers moves entire accounts between servers. Owner only.
	transfers := whm.Group("/transfers", middleware.RequirePermission("server.manage"))
	transfers.Get("/", middleware.RequirePermission("transfer.view"), h.Transfer.List)
	transfers.Post("/", middleware.RequirePermission("transfer.create"), h.Transfer.Create)
	transfers.Post("/test-connection", middleware.RequirePermission("transfer.create"), h.Transfer.TestConnection)
	transfers.Post("/discover", middleware.RequirePermission("transfer.create"), h.Transfer.Discover)
	// v3.1.48 — one-shot post-transfer recovery. Runs every
	// Rebuild*FromMongo (mailboxes, forwarders, ssh_keys, dns,
	// mysql access, ftp accounts, wp configs) so destination
	// filesystem / MySQL / PowerDNS / etc. match the just-imported
	// Mongo state. Idempotent — safe to call repeatedly. Same code
	// path the panel-records sync now calls automatically.
	transfers.Post("/rehydrate-all", middleware.RequirePermission("server.manage"), h.Transfer.RehydrateAll)
	// Transfer tokens — minted on the SOURCE panel and pasted into the
	// destination wizard so the destination never has to know the source's
	// root password. Same gate as the rest of the transfer surface.
	transfers.Get("/tokens", middleware.RequirePermission("transfer.view"), h.TransferTok.List)
	transfers.Post("/tokens", middleware.RequirePermission("transfer.create"), h.TransferTok.Issue)
	transfers.Delete("/tokens/:id", middleware.RequirePermission("transfer.manage"), h.TransferTok.Revoke)
	transfers.Get("/:id", middleware.RequirePermission("transfer.view"), h.Transfer.Get)
	transfers.Post("/:id/cancel", middleware.RequirePermission("transfer.manage"), h.Transfer.Cancel)

	// Public webhook receiver for Deploy Software projects. No auth middleware;
	// the handler verifies the per-project HMAC-SHA256 signature against the
	// raw body and rejects mismatches. Body is capped implicitly by Fiber's
	// BodyLimit (500 MB global) — a project webhook payload from GitHub is
	// always < 1 MB in practice so we don't lower it here.
	app.Post("/api/v1/deploy/webhooks/project/:project_id", h.Project.Webhook)

	// Public webhook receiver for App auto-deploy. Same shape as the
	// project webhook above — no auth middleware, HMAC-verified inside
	// the handler. Path uses the per-app WebhookID so the URL is
	// unguessable; mismatched signatures get a 401, missing IDs get a
	// 404 (deliberately indistinguishable from a deleted webhook).
	app.Post("/api/v1/webhooks/github/:id", h.Webhook.GitHubPush)

	// Developer surface — API tokens + outbound webhook subscriptions.
	// Inherits Auth + InjectScope + RateLimiter + AuditLogger from the
	// parent group, so revoke / rotate / create are all audited like the
	// rest of WHM.
	RegisterDeveloperRoutes(whm, &DeveloperHandlers{
		APIToken:     h.APIToken,
		WebhookEP:    h.WebhookEP,
		Programmatic: h.Programmatic,
	})

	// Mail Suite (separate product) — owner-only admin surface that
	// records registered mail-suite deployments and proxies the
	// per-domain "Enable Mail" / status calls to them. Does NOT
	// touch the existing email handlers or Roundcube SSO.
	if h.MailSuite != nil {
		ms := whm.Group("/mail-suite", middleware.RequireRole("vendor_owner"))
		ms.Get("/deployments", h.MailSuite.List)
		ms.Post("/deployments", h.MailSuite.Register)
		ms.Post("/install", h.MailSuite.Install)
		ms.Delete("/deployments/:id", h.MailSuite.Delete)
		ms.Get("/webmail-url", h.MailSuite.Webmail)
		ms.Post("/domains/:domain/enable-mail", h.MailSuite.EnableMail)
		ms.Get("/domains/:domain/status", h.MailSuite.Status)
	}
}
