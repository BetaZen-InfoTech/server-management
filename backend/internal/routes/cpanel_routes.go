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
	cpanel.Get("/domains/:id", h.Domain.Get)
	cpanel.Get("/domains/:id/stats", h.Domain.Stats)

	// Apps
	cpanel.Get("/apps", h.App.ListOwn)
	cpanel.Get("/apps/:name", h.App.Get)
	cpanel.Post("/apps/deploy", h.App.Deploy)
	cpanel.Get("/apps/:name/logs", h.App.Logs)

	// Databases
	cpanel.Get("/databases", h.Database.List)
	cpanel.Get("/databases/:id", h.Database.Get)
	cpanel.Post("/databases", h.Database.Create)
	cpanel.Delete("/databases/:id", h.Database.Delete)

	// Email
	cpanel.Get("/email", h.Email.ListMailboxes)
	cpanel.Get("/email/:id", h.Email.GetMailbox)
	cpanel.Post("/email", h.Email.CreateMailbox)
	cpanel.Put("/email/:id", h.Email.UpdateMailbox)
	cpanel.Delete("/email/:id", h.Email.DeleteMailbox)
	cpanel.Get("/email/forwarders", h.Email.ListForwarders)
	cpanel.Post("/email/forwarders", h.Email.CreateForwarder)

	// SSL
	cpanel.Get("/ssl", h.SSL.List)
	cpanel.Get("/ssl/:domain", h.SSL.Get)
	cpanel.Post("/ssl/letsencrypt", h.SSL.IssueLetsEncrypt)

	// Backups
	cpanel.Get("/backups", h.Backup.List)
	cpanel.Get("/backups/:id", h.Backup.Get)
	cpanel.Post("/backups", h.Backup.Create)
	cpanel.Get("/backups/schedules", h.Backup.ListSchedules)
	cpanel.Post("/backups/schedules", h.Backup.CreateSchedule)

	// WordPress
	cpanel.Get("/wordpress", h.WordPress.List)
	cpanel.Get("/wordpress/check-conflict", h.WordPress.CheckConflict)
	cpanel.Post("/wordpress/install", h.WordPress.Install)
	cpanel.Get("/wordpress/:id", h.WordPress.Get)
	cpanel.Get("/wordpress/:id/plugins", h.WordPress.ListPlugins)
	cpanel.Post("/wordpress/:id/plugins", h.WordPress.InstallPlugin)

	// Files
	cpanel.Get("/files/list", h.File.ListDir)
	cpanel.Get("/files/read", h.File.ReadFile)
	cpanel.Post("/files/create", h.File.CreateFile)
	cpanel.Post("/files/upload", h.File.Upload)
	cpanel.Put("/files/edit", h.File.EditFile)
	cpanel.Delete("/files/delete", h.File.DeleteFile)
	cpanel.Post("/files/rename", h.File.Rename)

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
	dns.Put("/zones/:domain/records/:id", h.DNS.UpdateRecord)
	dns.Delete("/zones/:domain/records/:id", h.DNS.DeleteRecord)
	dns.Get("/zones/:domain/export", h.DNS.ExportZone)

	// Deploy
	cpanel.Get("/deploy", h.Deploy.List)
	cpanel.Get("/deploy/:id", h.Deploy.Get)
	cpanel.Post("/deploy", h.Deploy.Create)
	cpanel.Get("/deploy/:id/logs", h.Deploy.Logs)

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
	packages.Get("/my-request", h.Package.MyRequest)
	packages.Post("/request-change", h.Package.RequestChange)
	packages.Get("/:id", h.Package.Get)

	// Team / users — lets a tenant-root (vendor_admin) manage their
	// own staff without the WHM sidebar. Tenant isolation is enforced
	// in the service layer via callerCtx(role, tenantID). Gated on
	// user.create so vendor_staff / customer / developer can't reach it.
	users := cpanel.Group("/users", middleware.RequirePermission("user.create"))
	users.Get("/", h.UserMgmt.List)
	users.Post("/", h.UserMgmt.Create)
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
