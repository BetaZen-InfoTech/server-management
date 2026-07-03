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

	api.Post("/dns/:domain/enable-mail", d.DNS.EnableMail)
	api.Get("/dns/:domain/status", d.DNS.Status)
}
