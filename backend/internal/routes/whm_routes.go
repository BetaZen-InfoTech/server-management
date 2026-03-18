package routes

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/handlers"
	"github.com/betazeninfotech/whm-cpanel-management/internal/middleware"
	"github.com/gofiber/fiber/v2"
)

type WHMHandlers struct {
	Domain       *handlers.DomainHandler
	App          *handlers.AppHandler
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
	Maintenance  *handlers.MaintenanceHandler
	Deploy       *handlers.DeployHandler
	User         *handlers.AuthHandler
	UserMgmt     *handlers.UserHandler
	Dashboard    *handlers.DashboardHandler
}

func RegisterWHMRoutes(app *fiber.App, cfg *config.Config, h *WHMHandlers) {
	whm := app.Group("/api/v1/whm",
		middleware.Auth(cfg),
		middleware.RequireRole("vendor_owner", "vendor_admin", "developer", "support"),
		middleware.RateLimiter(cfg.RateLimitWHM),
	)

	// Dashboard
	dashboard := whm.Group("/dashboard")
	dashboard.Get("/stats", h.Dashboard.WHMStats)
	dashboard.Get("/activity", h.Dashboard.WHMActivity)
	dashboard.Get("/server-status", h.Dashboard.WHMServerStatus)

	// Domains
	domains := whm.Group("/domains")
	domains.Get("/", middleware.RequirePermission("domain.view"), h.Domain.List)
	domains.Get("/:id", middleware.RequirePermission("domain.view"), h.Domain.Get)
	domains.Post("/", middleware.RequirePermission("domain.create"), h.Domain.Create)
	domains.Put("/:id", middleware.RequirePermission("domain.manage"), h.Domain.Update)
	domains.Delete("/:id", middleware.RequirePermission("domain.delete"), h.Domain.Delete)
	domains.Patch("/:id/suspend", middleware.RequirePermission("domain.manage"), h.Domain.Suspend)
	domains.Patch("/:id/unsuspend", middleware.RequirePermission("domain.manage"), h.Domain.Unsuspend)
	domains.Patch("/:id/php", middleware.RequirePermission("domain.manage"), h.Domain.SwitchPHP)
	domains.Get("/:id/stats", middleware.RequirePermission("domain.view"), h.Domain.Stats)

	// Apps
	apps := whm.Group("/apps")
	apps.Get("/", middleware.RequirePermission("app.view"), h.App.List)
	apps.Get("/:name", middleware.RequirePermission("app.view"), h.App.Get)
	apps.Post("/deploy", middleware.RequirePermission("app.deploy"), h.App.Deploy)
	apps.Post("/:name/redeploy", middleware.RequirePermission("app.deploy"), h.App.Redeploy)
	apps.Post("/:name/:action", middleware.RequirePermission("app.manage"), h.App.Action)
	apps.Delete("/:name", middleware.RequirePermission("app.manage"), h.App.Delete)
	apps.Get("/:name/logs", middleware.RequirePermission("app.view"), h.App.Logs)
	apps.Put("/:name/env", middleware.RequirePermission("app.manage"), h.App.UpdateEnv)
	apps.Post("/:name/rollback", middleware.RequirePermission("app.deploy"), h.App.Rollback)

	// Databases
	databases := whm.Group("/databases")
	databases.Get("/", middleware.RequirePermission("database.view"), h.Database.List)
	databases.Get("/:id", middleware.RequirePermission("database.view"), h.Database.Get)
	databases.Post("/", middleware.RequirePermission("database.create"), h.Database.Create)
	databases.Delete("/:id", middleware.RequirePermission("database.manage"), h.Database.Delete)
	databases.Get("/:id/users", middleware.RequirePermission("database.view"), h.Database.ListUsers)
	databases.Post("/:id/users", middleware.RequirePermission("database.manage"), h.Database.CreateUser)
	databases.Delete("/:id/users/:userId", middleware.RequirePermission("database.manage"), h.Database.DeleteUser)
	databases.Post("/:id/remote-access", middleware.RequirePermission("database.manage"), h.Database.EnableRemoteAccess)

	// Email (static routes before parameterized to avoid /:id catching "forwarders" etc.)
	email := whm.Group("/email")
	email.Get("/", middleware.RequirePermission("email.view"), h.Email.ListMailboxes)
	email.Post("/", middleware.RequirePermission("email.create"), h.Email.CreateMailbox)
	email.Get("/forwarders", middleware.RequirePermission("email.view"), h.Email.ListForwarders)
	email.Post("/forwarders", middleware.RequirePermission("email.manage"), h.Email.CreateForwarder)
	email.Delete("/forwarders/:id", middleware.RequirePermission("email.manage"), h.Email.DeleteForwarder)
	email.Put("/spam-settings/:domain", middleware.RequirePermission("email.manage"), h.Email.UpdateSpamSettings)
	email.Post("/dkim/:domain", middleware.RequirePermission("email.manage"), h.Email.SetupDKIM)
	email.Get("/:id", middleware.RequirePermission("email.view"), h.Email.GetMailbox)
	email.Put("/:id", middleware.RequirePermission("email.manage"), h.Email.UpdateMailbox)
	email.Delete("/:id", middleware.RequirePermission("email.manage"), h.Email.DeleteMailbox)

	// DNS
	dns := whm.Group("/dns")
	dns.Get("/zones", middleware.RequirePermission("dns.view"), h.DNS.ListZones)
	dns.Get("/zones/:domain", middleware.RequirePermission("dns.view"), h.DNS.GetZone)
	dns.Post("/zones", middleware.RequirePermission("dns.manage"), h.DNS.CreateZone)
	dns.Delete("/zones/:domain", middleware.RequirePermission("dns.manage"), h.DNS.DeleteZone)
	dns.Get("/zones/:domain/records", middleware.RequirePermission("dns.view"), h.DNS.ListRecords)
	dns.Post("/zones/:domain/records", middleware.RequirePermission("dns.manage"), h.DNS.AddRecord)
	dns.Put("/zones/:domain/records/:id", middleware.RequirePermission("dns.manage"), h.DNS.UpdateRecord)
	dns.Delete("/zones/:domain/records/:id", middleware.RequirePermission("dns.manage"), h.DNS.DeleteRecord)
	dns.Get("/zones/:domain/export", middleware.RequirePermission("dns.view"), h.DNS.ExportZone)

	// SSL
	ssl := whm.Group("/ssl", middleware.RequirePermission("ssl.manage"))
	ssl.Get("/", h.SSL.List)
	ssl.Get("/:domain", h.SSL.Get)
	ssl.Post("/letsencrypt", h.SSL.IssueLetsEncrypt)
	ssl.Post("/custom", h.SSL.UploadCustom)
	ssl.Post("/:domain/renew", h.SSL.Renew)
	ssl.Post("/:domain/revoke", h.SSL.Revoke)
	ssl.Delete("/:domain", h.SSL.Delete)

	// Backups (static routes before parameterized to avoid /:id catching "schedules" etc.)
	backups := whm.Group("/backups")
	backups.Get("/", middleware.RequirePermission("backup.view"), h.Backup.List)
	backups.Post("/", middleware.RequirePermission("backup.create"), h.Backup.Create)
	backups.Post("/restore", middleware.RequirePermission("backup.restore"), h.Backup.Restore)
	backups.Get("/schedules", middleware.RequirePermission("backup.view"), h.Backup.ListSchedules)
	backups.Post("/schedules", middleware.RequirePermission("backup.create"), h.Backup.CreateSchedule)
	backups.Delete("/schedules/:id", middleware.RequirePermission("backup.create"), h.Backup.DeleteSchedule)
	backups.Get("/:id", middleware.RequirePermission("backup.view"), h.Backup.Get)
	backups.Delete("/:id", middleware.RequirePermission("backup.create"), h.Backup.Delete)
	backups.Get("/:id/download", middleware.RequirePermission("backup.view"), h.Backup.Download)

	// WordPress
	wp := whm.Group("/wordpress", middleware.RequirePermission("wordpress.manage"))
	wp.Get("/", h.WordPress.List)
	wp.Get("/:id", h.WordPress.Get)
	wp.Post("/install", h.WordPress.Install)
	wp.Delete("/:id", h.WordPress.Delete)
	wp.Post("/:id/update", h.WordPress.Update)
	wp.Post("/:id/security-scan", h.WordPress.SecurityScan)
	wp.Get("/:id/plugins", h.WordPress.ListPlugins)
	wp.Post("/:id/plugins", h.WordPress.InstallPlugin)
	wp.Patch("/:id/maintenance", h.WordPress.ToggleMaintenance)

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
	sw := whm.Group("/software")
	sw.Get("/installed", h.Software.ListInstalled)
	sw.Get("/packages", h.Software.ListInstalled)
	sw.Post("/install", h.Software.Install)
	sw.Post("/uninstall", h.Software.Uninstall)
	sw.Get("/updates", h.Software.CheckUpdates)
	sw.Post("/install-email", h.Software.InstallEmail)
	sw.Get("/email-status", h.Software.EmailStatus)
	sw.Get("/email-installation/:id", h.Software.GetEmailInstallation)
	sw.Put("/email-settings", h.Software.UpdateEmailSettings)

	// Monitoring
	monitor := whm.Group("/monitor", middleware.RequirePermission("monitor.view"))
	monitor.Get("/system", h.Monitoring.SystemInfo)
	monitor.Get("/metrics", h.Monitoring.Metrics)
	monitor.Get("/services", h.Monitoring.Services)
	monitor.Get("/history", h.Monitoring.History)
	monitor.Get("/alerts", h.Monitoring.GetAlerts)
	monitor.Put("/alerts", h.Monitoring.UpdateAlerts)

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
	files.Post("/rename", h.File.Rename)
	files.Post("/chmod", h.File.Chmod)
	files.Post("/compress", h.File.Compress)
	files.Post("/extract", h.File.Extract)

	// SSH Keys
	sshKeys := whm.Group("/ssh-keys", middleware.RequirePermission("ssh.manage"))
	sshKeys.Get("/:user", h.SSHKey.List)
	sshKeys.Post("/:user", h.SSHKey.Add)
	sshKeys.Delete("/:user/:id", h.SSHKey.Delete)
	sshKeys.Post("/:user/generate", h.SSHKey.Generate)

	// Processes (static routes before parameterized to avoid /:pid catching "services")
	procs := whm.Group("/processes")
	procs.Get("/", middleware.RequirePermission("process.view"), h.Process.List)
	procs.Get("/services", middleware.RequirePermission("process.view"), h.Process.ListServices)
	procs.Post("/services/:name/:action", middleware.RequirePermission("process.manage"), h.Process.ControlService)
	procs.Get("/:pid", middleware.RequirePermission("process.view"), h.Process.Get)
	procs.Post("/:pid/kill", middleware.RequirePermission("process.manage"), h.Process.Kill)

	// Resources
	res := whm.Group("/resources")
	res.Get("/summary", middleware.RequirePermission("server.view"), h.Resource.Summary)
	res.Get("/domains/:domain", middleware.RequirePermission("domain.view"), h.Resource.DomainUsage)
	res.Get("/bandwidth", middleware.RequirePermission("server.view"), h.Resource.Bandwidth)
	res.Get("/bandwidth/:domain", middleware.RequirePermission("domain.view"), h.Resource.BandwidthByDomain)
	res.Put("/domains/:domain/limits", middleware.RequirePermission("domain.manage"), h.Resource.UpdateLimits)

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
	serverCfg.Post("/nginx/test", h.Config.TestNginx)
	serverCfg.Post("/:service/restart", h.Config.RestartService)

	// Maintenance
	maint := whm.Group("/maintenance")
	maint.Get("/", middleware.RequirePermission("server.view"), h.Maintenance.Status)
	maint.Post("/enable", middleware.RequirePermission("server.manage"), h.Maintenance.EnableServer)
	maint.Post("/disable", middleware.RequirePermission("server.manage"), h.Maintenance.DisableServer)
	maint.Post("/domains/:domain/enable", middleware.RequirePermission("domain.manage"), h.Maintenance.EnableDomain)
	maint.Post("/domains/:domain/disable", middleware.RequirePermission("domain.manage"), h.Maintenance.DisableDomain)

	// GitHub Deploy
	deploy := whm.Group("/deploy", middleware.RequirePermission("deploy.manage"))
	deploy.Get("/", h.Deploy.List)
	deploy.Get("/:id", h.Deploy.Get)
	deploy.Post("/", h.Deploy.Create)
	deploy.Post("/:id/redeploy", h.Deploy.Redeploy)
	deploy.Post("/:id/rollback", h.Deploy.Rollback)
	deploy.Post("/:id/cancel", h.Deploy.Cancel)
	deploy.Delete("/:id", h.Deploy.Delete)
	deploy.Get("/:id/logs", h.Deploy.Logs)
	deploy.Get("/:id/history", h.Deploy.History)
	deploy.Post("/:id/pause", h.Deploy.Pause)
	deploy.Post("/:id/resume", h.Deploy.Resume)

	// Users
	users := whm.Group("/users", middleware.RequirePermission("server.manage"))
	users.Get("/", h.UserMgmt.List)
	users.Post("/", h.UserMgmt.Create)
	users.Post("/:id/suspend", h.UserMgmt.Suspend)
	users.Post("/:id/activate", h.UserMgmt.Activate)
	users.Delete("/:id", h.UserMgmt.Delete)

	// GitHub webhook receiver (public, verified by signature)
	app.Post("/api/v1/deploy/webhooks/github", h.Deploy.GitHubWebhook)
}
