package routes

import (
	"github.com/betazeninfotech/mail-suite/internal/handlers"
	"github.com/betazeninfotech/mail-suite/internal/middleware"
	"github.com/betazeninfotech/mail-suite/pkg/jwt"
	"github.com/gofiber/fiber/v2"
)

type Deps struct {
	JWT       *jwt.Manager
	Auth      *handlers.AuthHandler
	Account   *handlers.AccountHandler
	Mail      *handlers.MailHandler
	Signature *handlers.SignatureHandler
	Forwarder *handlers.ForwarderHandler
	DNS       *handlers.DNSHandler
	Push      *handlers.PushHandler
	Draft        *handlers.DraftHandler
	Tracking     *handlers.TrackingHandler
	Contact          *handlers.ContactHandler
	ContactGroup     *handlers.ContactGroupHandler
	Campaign         *handlers.CampaignHandler
	CampaignTemplate *handlers.CampaignTemplateHandler
}

func Register(app *fiber.App, d Deps) {
	api := app.Group("/api/v1")

	// Public
	auth := api.Group("/auth")
	auth.Post("/register", d.Auth.Register)
	auth.Post("/login", d.Auth.Login)
	auth.Post("/refresh", d.Auth.Refresh)
	auth.Post("/logout", d.Auth.Logout)

	// Protected
	api.Use(middleware.Auth(d.JWT))

	api.Get("/auth/me", d.Auth.Me)

	api.Get("/accounts", d.Account.List)
	api.Post("/accounts", d.Account.Create)
	api.Post("/accounts/test", d.Account.Test)
	api.Patch("/accounts/:id", d.Account.Update)
	api.Delete("/accounts/:id", d.Account.Delete)
	api.Post("/accounts/:id/primary", d.Account.SetPrimary)

	api.Get("/mail/:account_id/folders", d.Mail.Folders)
	api.Get("/mail/:account_id/threads", d.Mail.Threads)
	api.Get("/mail/:account_id/messages/:uid", d.Mail.Message)
	api.Patch("/mail/:account_id/messages/:uid", d.Mail.Flag)
	api.Post("/mail/:account_id/send", d.Mail.Send)

	api.Get("/signatures", d.Signature.List)
	api.Post("/signatures", d.Signature.Create)
	api.Put("/signatures/:id", d.Signature.Update)
	api.Delete("/signatures/:id", d.Signature.Delete)

	api.Get("/forwarders", d.Forwarder.List)
	api.Post("/forwarders", d.Forwarder.Create)
	api.Delete("/forwarders/:id", d.Forwarder.Delete)

	api.Post("/devices", d.Push.Register)
	api.Delete("/devices/:id", d.Push.Delete)

	api.Get("/drafts", d.Draft.List)
	api.Post("/drafts", d.Draft.Create)
	api.Get("/drafts/:id", d.Draft.Get)
	api.Put("/drafts/:id", d.Draft.Update)
	api.Delete("/drafts/:id", d.Draft.Delete)

	api.Get("/tracking/sent", d.Tracking.ListSent)
	api.Get("/tracking/message", d.Tracking.MessageDetail)
	api.Get("/tracking/sent/:track_id", d.Tracking.Detail)

	api.Get("/contacts", d.Contact.List)
	api.Post("/contacts", d.Contact.Create)
	api.Post("/contacts/import", d.Contact.Import)
	api.Get("/contacts/:id", d.Contact.Get)
	api.Put("/contacts/:id", d.Contact.Update)
	api.Delete("/contacts/:id", d.Contact.Delete)

	api.Get("/contact-groups", d.ContactGroup.List)
	api.Post("/contact-groups", d.ContactGroup.Create)
	api.Put("/contact-groups/:id", d.ContactGroup.Update)
	api.Delete("/contact-groups/:id", d.ContactGroup.Delete)

	api.Get("/campaigns", d.Campaign.List)
	api.Post("/campaigns", d.Campaign.Create)
	api.Get("/campaigns/:id", d.Campaign.Get)
	api.Put("/campaigns/:id", d.Campaign.Update)
	api.Delete("/campaigns/:id", d.Campaign.Delete)
	api.Post("/campaigns/:id/start", d.Campaign.Start)
	api.Post("/campaigns/:id/pause", d.Campaign.Pause)
	api.Post("/campaigns/:id/cancel", d.Campaign.Cancel)
	api.Get("/campaigns/:id/stats", d.Campaign.Stats)
	api.Get("/campaigns/:id/recipients", d.Campaign.Recipients)

	api.Get("/campaign-templates", d.CampaignTemplate.List)
	api.Post("/campaign-templates", d.CampaignTemplate.Create)
	api.Delete("/campaign-templates/:id", d.CampaignTemplate.Delete)

	api.Post("/dns/:domain/enable-mail", d.DNS.EnableMail)
	api.Get("/dns/:domain/status", d.DNS.Status)
}
