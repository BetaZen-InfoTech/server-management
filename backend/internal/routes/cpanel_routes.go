package routes

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/middleware"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/mongo"
)

func RegisterCPanelRoutes(app *fiber.App, cfg *config.Config, db *mongo.Database, h *WHMHandlers) {
	// The User Panel serves every non-owner role — vendors (vendor_admin),
	// their team (vendor_staff), developers and support agents invited by
	// a vendor, and end customers. vendor_owner stays out: the platform
	// owner belongs on /whm/*, and login.tsx already bounces them there
	// if they try the User Panel login. Per-route tenant scoping is
	// handled by the handlers via InjectScope.
	cpanel := app.Group("/api/v1/cpanel",
		middleware.Auth(cfg, db),
		middleware.InjectScope(),
		middleware.RequireRole("vendor_admin", "vendor_staff", "developer", "support", "customer"),
		middleware.RateLimiter(cfg.RateLimitCPanel),
	)

	// Dashboard
	cpanel.Get("/dashboard/stats", h.Dashboard.CPanelStats)
	cpanel.Get("/dashboard/activity", h.Dashboard.CPanelActivity)

	// Domains (own domains only)
	cpanel.Get("/domains", h.Domain.ListOwn)
	cpanel.Post("/domains", h.Domain.CPanelCreate)
	cpanel.Get("/domains/:id", h.Domain.Get)
	cpanel.Delete("/domains/:id", h.Domain.CPanelDelete)
	cpanel.Get("/domains/:id/stats", h.Domain.Stats)

	// Apps — full WHM parity. Tenant scope is enforced in the service
	// layer via CallerScope on every lookup, so a vendor can only see
	// and mutate their own apps even though handler methods are shared.
	// Specific routes registered before /:name/:action to avoid Fiber's
	// prefix matching swallowing concrete verbs as action names.
	cpanel.Get("/apps", h.App.ListOwn)
	cpanel.Get("/apps/presets", h.App.Presets)
	cpanel.Post("/apps/deploy", h.App.Deploy)
	cpanel.Get("/apps/:name", h.App.Get)
	cpanel.Put("/apps/:name", h.App.Update)
	cpanel.Delete("/apps/:name", h.App.Delete)
	cpanel.Get("/apps/:name/logs", h.App.Logs)
	cpanel.Get("/apps/:name/backups", h.App.ListBackups)
	cpanel.Post("/apps/:name/backup", h.App.Backup)
	cpanel.Post("/apps/:name/restore", h.App.Restore)
	cpanel.Post("/apps/:name/transfer", h.App.Transfer)
	cpanel.Post("/apps/:name/redeploy", h.App.Redeploy)
	cpanel.Post("/apps/:name/install-packages", h.App.InstallPackages)
	cpanel.Post("/apps/:name/rollback", h.App.Rollback)
	cpanel.Put("/apps/:name/env", h.App.UpdateEnv)
	cpanel.Post("/apps/:name/:action", h.App.Action)

	// Databases — vendor parity with WHM for both MySQL and MongoDB.
	// Tenant isolation is enforced at the service layer (scope.AssertOwnsDomain
	// on each :id lookup), so a vendor can only touch their own databases,
	// users, access hosts and phpMyAdmin SSO tokens even though the routes
	// share handler methods with WHM.
	cpanel.Get("/databases", h.Database.List)
	cpanel.Get("/databases/:id", h.Database.Get)
	cpanel.Post("/databases", h.Database.Create)
	cpanel.Delete("/databases/:id", h.Database.Delete)
	cpanel.Get("/databases/:id/connection", h.Database.GetConnection)
	cpanel.Get("/databases/:id/phpmyadmin", h.Database.GetPhpMyAdmin)
	cpanel.Put("/databases/:id/password", h.Database.UpdateOwnerPassword)
	cpanel.Get("/databases/:id/users", h.Database.ListUsers)
	cpanel.Post("/databases/:id/users", h.Database.CreateUser)
	cpanel.Put("/databases/:id/users/:userId/password", h.Database.UpdateUserPassword)
	cpanel.Put("/databases/:id/users/:userId/role", h.Database.UpdateUserRole)
	cpanel.Delete("/databases/:id/users/:userId", h.Database.DeleteUser)
	cpanel.Get("/databases/:id/access-hosts", h.Database.ListAccessHosts)
	cpanel.Post("/databases/:id/access-hosts", h.Database.AddAccessHost)
	cpanel.Delete("/databases/:id/access-hosts/:hostId", h.Database.RemoveAccessHost)

	// Email — static routes (forwarders / spam-settings / dkim / webmail-token)
	// must be registered before parameterised /:id to keep Fiber's router
	// from matching literal segments as a mailbox id. Mirrors WHM's group.
	cpanel.Get("/email", h.Email.ListMailboxes)
	cpanel.Post("/email", h.Email.CreateMailbox)
	cpanel.Get("/email/forwarders", h.Email.ListForwarders)
	cpanel.Post("/email/forwarders", h.Email.CreateForwarder)
	cpanel.Delete("/email/forwarders/:id", h.Email.DeleteForwarder)
	cpanel.Put("/email/spam-settings/:domain", h.Email.UpdateSpamSettings)
	cpanel.Post("/email/dkim/:domain", h.Email.SetupDKIM)
	cpanel.Post("/email/webmail-token", h.Email.WebmailToken)
	// Test-email for a single mailbox. Tenant scope is enforced in the
	// service layer via GetMailbox's domain lookup — vendors can only
	// test their own mailboxes. Static route BEFORE /:id.
	cpanel.Post("/email/:id/test", h.Email.SendTest)
	cpanel.Get("/email/:id", h.Email.GetMailbox)
	cpanel.Put("/email/:id", h.Email.UpdateMailbox)
	cpanel.Delete("/email/:id", h.Email.DeleteMailbox)

	// SSL — handler methods key off :domain (the CN), not an ObjectID.
	// The previous `/ssl/:id/...` registration was a bug: c.Params("domain")
	// read an empty string and the service fell through to "SSL not found".
	// Tenant scope is enforced in GetByDomain via scope.AssertOwnsDomain,
	// so a vendor can only renew/revoke/delete certs on domains they own.
	cpanel.Get("/ssl", h.SSL.List)
	cpanel.Get("/ssl/:domain", h.SSL.Get)
	cpanel.Post("/ssl/letsencrypt", h.SSL.IssueLetsEncrypt)
	// Bulk SSL — installs serially per domain. Static path before the
	// /:domain param routes so the Fiber router matches it correctly.
	cpanel.Post("/ssl/letsencrypt/bulk", h.SSL.IssueLetsEncryptBulk)
	cpanel.Post("/ssl/custom", h.SSL.UploadCustom)
	cpanel.Post("/ssl/:domain/renew", h.SSL.Renew)
	cpanel.Post("/ssl/:domain/revoke", h.SSL.Revoke)
	cpanel.Post("/ssl/:domain/force-ssl", h.SSL.ForceSSL)
	cpanel.Delete("/ssl/:domain", h.SSL.Delete)

	// Backups
	cpanel.Get("/backups", h.Backup.List)
	cpanel.Get("/backups/:id", h.Backup.Get)
	cpanel.Post("/backups", h.Backup.Create)
	cpanel.Delete("/backups/:id", h.Backup.Delete)
	cpanel.Get("/backups/:id/download", h.Backup.Download)
	cpanel.Post("/backups/:id/restore", h.Backup.CPanelRestore)
	cpanel.Get("/backups/schedules", h.Backup.ListSchedules)
	cpanel.Post("/backups/schedules", h.Backup.CreateSchedule)

	// WordPress — vendor parity with WHM. Tenant isolation is enforced
	// at the service layer: WordPressService.GetByID now applies
	// scope.ApplyTo before every id-based mutation (Delete / Update /
	// plugin ops / toggles / AutoLogin / SecurityScan), so a vendor can
	// only touch their own installs. Static paths (check-conflict /
	// install / rescan) are registered before parameterised /:id to
	// keep Fiber's router from swallowing them as ids.
	cpanel.Get("/wordpress", h.WordPress.List)
	cpanel.Get("/wordpress/check-conflict", h.WordPress.CheckConflict)
	cpanel.Post("/wordpress/install", h.WordPress.Install)
	cpanel.Post("/wordpress/rescan", h.WordPress.Rescan)
	cpanel.Get("/wordpress/:id", h.WordPress.Get)
	cpanel.Post("/wordpress/:id/update", h.WordPress.Update)
	cpanel.Delete("/wordpress/:id", h.WordPress.Delete)
	cpanel.Post("/wordpress/:id/security-scan", h.WordPress.SecurityScan)
	cpanel.Patch("/wordpress/:id/maintenance", h.WordPress.ToggleMaintenance)
	cpanel.Patch("/wordpress/:id/auto-update", h.WordPress.ToggleAutoUpdate)
	cpanel.Post("/wordpress/:id/auto-login", h.WordPress.AutoLogin)
	cpanel.Get("/wordpress/:id/plugins", h.WordPress.ListPlugins)
	cpanel.Post("/wordpress/:id/plugins", h.WordPress.InstallPlugin)
	// Per-plugin actions. "update" is registered before the /:slug
	// routes so Fiber doesn't try to match it as a slug.
	cpanel.Post("/wordpress/:id/plugins/update", h.WordPress.UpdatePlugin)
	cpanel.Post("/wordpress/:id/plugins/:slug/activate", h.WordPress.ActivatePlugin)
	cpanel.Post("/wordpress/:id/plugins/:slug/deactivate", h.WordPress.DeactivatePlugin)
	cpanel.Post("/wordpress/:id/plugins/:slug/update", h.WordPress.UpdatePlugin)
	cpanel.Delete("/wordpress/:id/plugins/:slug", h.WordPress.DeletePlugin)
	// Themes — same shape as plugins.
	cpanel.Get("/wordpress/:id/themes", h.WordPress.ListThemes)
	cpanel.Post("/wordpress/:id/themes", h.WordPress.InstallTheme)
	cpanel.Post("/wordpress/:id/themes/update", h.WordPress.UpdateTheme)
	cpanel.Post("/wordpress/:id/themes/:slug/activate", h.WordPress.ActivateTheme)
	cpanel.Post("/wordpress/:id/themes/:slug/update", h.WordPress.UpdateTheme)
	cpanel.Delete("/wordpress/:id/themes/:slug", h.WordPress.DeleteTheme)
	// Detach — drop tracking row, preserve files + DB on disk.
	cpanel.Post("/wordpress/:id/detach", h.WordPress.Detach)
	cpanel.Get("/wordpress/:id/users", h.WordPress.ListUsers)
	cpanel.Post("/wordpress/:id/users", h.WordPress.CreateUser)
	cpanel.Delete("/wordpress/:id/users/:uid", h.WordPress.DeleteUser)
	cpanel.Patch("/wordpress/:id/users/:uid", h.WordPress.UpdateUserRole)

	// Files
	cpanel.Get("/files/list", h.File.ListDir)
	cpanel.Get("/files/read", h.File.ReadFile)
	cpanel.Post("/files/create", h.File.CreateFile)
	cpanel.Post("/files/upload", h.File.Upload)
	cpanel.Put("/files/edit", h.File.EditFile)
	cpanel.Delete("/files/delete", h.File.DeleteFile)
	// Trash (soft-delete inbox) — mirrors the WHM surface. Tenant
	// isolation is enforced at the service layer (resolveCallerUser
	// defaults to the caller's own username), so vendors can only see
	// and restore from their own .Trash directory.
	cpanel.Get("/files/trash", h.File.ListTrash)
	cpanel.Post("/files/trash/restore", h.File.RestoreTrash)
	cpanel.Delete("/files/trash", h.File.DeleteTrash)
	cpanel.Post("/files/rename", h.File.Rename)
	// Extended file-manager ops — tenant isolation is enforced at the service
	// layer via assertTenantOwnsUser / resolveCallerUser, so these mirror the
	// WHM file-manager surface and give a vendor full ops parity inside their
	// own /home jail.
	cpanel.Get("/files/search", h.File.Search)
	cpanel.Get("/files/download", h.File.Download)
	cpanel.Get("/files/info", h.File.Info)
	cpanel.Post("/files/chmod", h.File.Chmod)
	cpanel.Post("/files/compress", h.File.Compress)
	cpanel.Post("/files/extract", h.File.Extract)
	cpanel.Post("/files/copy", h.File.Copy)
	cpanel.Post("/files/move", h.File.Move)
	cpanel.Post("/files/protect", h.File.PasswordProtect)
	cpanel.Post("/files/unprotect", h.File.Unprotect)

	// Cron Jobs
	cpanel.Get("/cron", h.Cron.CPanelList)
	cpanel.Post("/cron", h.Cron.CPanelCreate)
	cpanel.Put("/cron/:id", h.Cron.Update)
	cpanel.Delete("/cron/:id", h.Cron.Delete)
	cpanel.Patch("/cron/:id/toggle", h.Cron.Toggle)

	// Resources
	cpanel.Get("/resources/domains/:domain", h.Resource.DomainUsage)
	cpanel.Get("/resources/bandwidth/:domain", h.Resource.BandwidthByDomain)

	// DNS — tenant-scoped. Missing on cPanel for a long while, which is
	// why /user-panel/dns was returning "Failed to load DNS zones" —
	// the route simply didn't exist. ListZones filters by
	// CallerScope.TenantDomains; every per-zone method calls
	// assertCallerOwnsDomain, so a vendor_admin can only see/edit zones
	// for domains their tenant owns. CreateZone / DeleteZone stay
	// owner-only on WHM — zones are auto-provisioned when a domain is
	// added, and whole-zone delete is destructive.
	dns := cpanel.Group("/dns")
	dns.Get("/zones", h.DNS.ListZones)
	dns.Get("/zones/:domain", h.DNS.GetZone)
	dns.Get("/zones/:domain/records", h.DNS.ListRecords)
	dns.Post("/zones/:domain/records", h.DNS.AddRecord)
	// Bulk-add: register BEFORE the /:id routes so Fiber doesn't match
	// "bulk" as an ObjectID. Used by the inline-edit "Save All Records"
	// button to commit N pending rows in one round-trip.
	dns.Post("/zones/:domain/records/bulk", h.DNS.BulkAddRecords)
	dns.Put("/zones/:domain/records/:id", h.DNS.UpdateRecord)
	dns.Delete("/zones/:domain/records/:id", h.DNS.DeleteRecord)
	dns.Get("/zones/:domain/export", h.DNS.ExportZone)

	// Deploy
	cpanel.Get("/deploy", h.Deploy.List)
	cpanel.Get("/deploy/:id", h.Deploy.Get)
	cpanel.Post("/deploy", h.Deploy.Create)
	cpanel.Get("/deploy/:id/logs", h.Deploy.Logs)
	cpanel.Delete("/deploy/:id", h.Deploy.Delete)
	cpanel.Post("/deploy/:id/redeploy", h.Deploy.Redeploy)
	cpanel.Get("/deploy/:id/history", h.Deploy.History)

	// Deploy Software (Projects) — vendor parity with WHM. Tenant scope
	// is enforced in the project service via CallerScope on every
	// lookup; a vendor can only see and mutate their own projects and
	// services. Specific per-project paths and per-service action routes
	// are registered before the broader parameterised ones to avoid the
	// router matching "action"/"deploy"/"services" as a service id.
	projects := cpanel.Group("/projects")
	projects.Get("/", h.Project.List)
	projects.Post("/", h.Project.Create)
	projects.Post("/provision", h.Project.Provision)
	projects.Get("/:id", h.Project.Get)
	projects.Put("/:id", h.Project.Update)
	projects.Delete("/:id", h.Project.Delete)
	projects.Post("/:id/deploy", h.Project.DeployAll)
	projects.Post("/:id/rotate-pat", h.Project.RotatePAT)
	projects.Post("/:id/pause", h.Project.Pause)
	projects.Post("/:id/resume", h.Project.Resume)
	projects.Post("/:id/action/:action", h.Project.ProjectAction)
	projects.Get("/:id/webhook", h.Project.WebhookInfo)
	projects.Get("/:id/activity", h.Project.Activity)
	projects.Get("/:id/services", h.Project.ListServices)
	projects.Post("/:id/services", h.Project.AddService)
	projects.Put("/:id/services/:svc", h.Project.UpdateService)
	projects.Delete("/:id/services/:svc", h.Project.RemoveService)
	projects.Post("/:id/services/:svc/deploy", h.Project.DeployService)
	projects.Get("/:id/services/:svc/deployments/latest", h.Project.LatestDeployment)
	projects.Post("/:id/services/:svc/action/:action", h.Project.ServiceAction)
	projects.Get("/:id/services/:svc/logs", h.Project.Logs)
	projects.Post("/:id/services/:svc/aliases", h.Project.AddAlias)
	projects.Delete("/:id/services/:svc/aliases/:domain", h.Project.RemoveAlias)

	// SSH Keys (own keys)
	sshKeys := cpanel.Group("/ssh-keys")
	sshKeys.Get("/", h.SSHKey.CPanelList)
	sshKeys.Post("/", h.SSHKey.CPanelAdd)
	sshKeys.Delete("/:id", h.SSHKey.CPanelDelete)
	sshKeys.Post("/generate", h.SSHKey.CPanelGenerate)

	// Audit (own actions). List + Get; tenant scope enforced inside the
	// service. Export stays owner-only on WHM.
	cpanel.Get("/audit", h.Audit.List)
	cpanel.Get("/audit/:id", h.Audit.Get)

	// Packages — vendor-facing subset. They can browse the catalog,
	// see their current package, and submit a plan-switch request.
	// Admin CRUD + the approve/reject queue stay on WHM.
	packages := cpanel.Group("/packages")
	packages.Get("/", h.Package.List)
	// Static routes must be registered before /:id so Fiber doesn't
	// try to parse "my-request" / "my-package" as an ObjectID.
	packages.Get("/my-request", h.Package.MyRequest)
	packages.Get("/my-package", h.Package.MyPackage)
	packages.Post("/request-change", h.Package.RequestChange)
	packages.Get("/:id", h.Package.Get)

	// Team / users — lets a tenant-root (vendor_admin) manage their
	// own staff without the WHM sidebar. Tenant isolation is enforced
	// in the service layer via callerCtx(role, tenantID). Gated on
	// user.create so vendor_staff / customer / developer can't reach it.
	users := cpanel.Group("/users", middleware.RequirePermission("user.create"))
	users.Get("/", h.UserMgmt.List)
	users.Post("/", h.UserMgmt.Create)
	// Shell-access management — static paths before parameterised /:id
	// ones so Fiber's router matches the literal "shell-access" segment
	// first. Tenant isolation is enforced in user_service: a vendor can
	// only toggle the shell for users whose tenant_id matches theirs.
	users.Get("/shell-access", h.UserMgmt.ListShellAccess)
	users.Post("/:id/shell-access", h.UserMgmt.SetShellAccess)
	users.Post("/:id/suspend", h.UserMgmt.Suspend)
	users.Post("/:id/activate", h.UserMgmt.Activate)
	users.Post("/:id/reset-password", h.UserMgmt.ResetPassword)
	users.Get("/:id", h.UserMgmt.Get)
	users.Put("/:id", h.UserMgmt.Update)
	users.Delete("/:id", h.UserMgmt.Delete)

	// Notifications — per-user settings + history. Shared handler with
	// WHM; each user only sees their own rows because the service keys
	// on user_id from context.
	notif := cpanel.Group("/notifications")
	notif.Get("/settings", h.Notification.GetSettings)
	notif.Put("/settings", h.Notification.UpdateSettings)
	notif.Get("/history", h.Notification.History)

	// Logs — tenant-scoped (commit acfc8d9). A vendor_admin/staff only
	// sees log files for domains owned by their tenant.
	logs := cpanel.Group("/logs", middleware.RequirePermission("log.view"))
	logs.Get("/files", h.Log.ListFiles)
	logs.Get("/:type", h.Log.View)
	logs.Get("/:type/search", h.Log.Search)
	logs.Get("/:type/download", h.Log.Download)
}
