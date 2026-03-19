package routes

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/middleware"
	"github.com/gofiber/fiber/v2"
)

func RegisterCPanelRoutes(app *fiber.App, cfg *config.Config, h *WHMHandlers) {
	cpanel := app.Group("/api/v1/cpanel",
		middleware.Auth(cfg),
		middleware.RequireRole("customer"),
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
	cpanel.Get("/wordpress/:id", h.WordPress.Get)
	cpanel.Post("/wordpress/install", h.WordPress.Install)
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

	// Deploy
	cpanel.Get("/deploy", h.Deploy.List)
	cpanel.Get("/deploy/:id", h.Deploy.Get)
	cpanel.Post("/deploy", h.Deploy.Create)
	cpanel.Get("/deploy/:id/logs", h.Deploy.Logs)

	// Audit (own actions)
	cpanel.Get("/audit", h.Audit.List)
}
