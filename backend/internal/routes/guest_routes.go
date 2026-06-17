// Package routes — guest_routes.go wires the no-login, single-domain guest
// surface that the one-time magic link drives.
//
//   - POST /api/v1/guest/redeem is PUBLIC (rate-limited): it claims the link
//     for the first browser, sets the session cookies, and returns the scoped
//     view. It is intentionally registered OUTSIDE the GuestAuth group.
//   - Everything else under /api/v1/guest/* is gated by GuestAuth and is
//     hard-scoped to the single domain stored on the session — there is no
//     :domain path param, and no domain-list / cross-domain / zone routes are
//     registered, so "show no other data" holds by construction.
package routes

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/handlers"
	"github.com/betazeninfotech/whm-cpanel-management/internal/middleware"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/gofiber/fiber/v2"
)

func RegisterGuestRoutes(app *fiber.App, cfg *config.Config, guestSvc *services.GuestLinkService, h *handlers.GuestHandler) {
	// Public first-open / redeem. LoginRateLimiter caps brute force per IP;
	// bcrypt only runs after an indexed link_id hit inside Verify.
	app.Post("/api/v1/guest/redeem", middleware.LoginRateLimiter(), h.Redeem)

	g := app.Group("/api/v1/guest",
		middleware.GuestAuth(cfg, guestSvc),
		middleware.RateLimiter(cfg.RateLimitCPanel),
	)
	g.Post("/logout", h.Logout)
	g.Get("/session", h.Session)

	// Email (every link type).
	g.Get("/mailboxes", h.ListMailboxes)
	g.Post("/mailboxes", h.CreateMailbox)
	g.Patch("/mailboxes/:addr", h.UpdateMailbox)
	g.Delete("/mailboxes/:addr", h.DeleteMailbox)
	g.Post("/mailboxes/:addr/password", h.ResetMailboxPassword)
	g.Post("/mailboxes/:addr/webmail-link", h.WebmailLink)
	g.Get("/forwarders", h.ListForwarders)
	g.Post("/forwarders", h.CreateForwarder)
	g.Delete("/forwarders/:id", h.DeleteForwarder)

	// DNS (handlers 403 unless the link is email_dns / a main domain).
	g.Get("/dns/records", h.ListDNSRecords)
	g.Post("/dns/records", h.CreateDNSRecord)
	g.Patch("/dns/records/:id", h.UpdateDNSRecord)
	g.Delete("/dns/records/:id", h.DeleteDNSRecord)
}
