// Package routes — api_routes.go wires three things:
//
//  1. /api/v1/whm/developer/* and /api/v1/cpanel/developer/* — the
//     JWT-authenticated surface for managing API tokens and webhooks. Lives
//     under the existing whm/cpanel groups so it inherits Auth, InjectScope,
//     RateLimiter, and AuditLogger from the parent.
//
//  2. /api/v1/external/* — the token-authenticated programmatic surface used
//     by API-token bearers. Each endpoint declares the scope it needs via
//     middleware.RequireTokenScope.
//
// Both surfaces reuse the panel's existing services so behaviour and tenant
// scoping stay identical between human and programmatic access.
package routes

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/handlers"
	"github.com/betazeninfotech/whm-cpanel-management/internal/middleware"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/mongo"
)

// DeveloperHandlers is the small handler bundle for the Developer page.
type DeveloperHandlers struct {
	APIToken     *handlers.APITokenHandler
	WebhookEP    *handlers.WebhookEndpointHandler
	Programmatic *handlers.ProgrammaticHandler
}

// RegisterDeveloperRoutes wires the JWT-authenticated developer-management
// surface onto the parent group. Both WHM and cPanel routes get an identical
// set of endpoints; tenant scoping in the services keeps vendor visibility
// limited to their own rows.
func RegisterDeveloperRoutes(parent fiber.Router, h *DeveloperHandlers) {
	dev := parent.Group("/developer")

	tokens := dev.Group("/tokens")
	tokens.Get("/scopes", h.APIToken.Scopes)
	tokens.Get("/", h.APIToken.List)
	tokens.Post("/", h.APIToken.Create)
	tokens.Post("/:id/rotate", h.APIToken.Rotate)
	tokens.Delete("/:id", h.APIToken.Revoke)

	webhooks := dev.Group("/webhooks")
	webhooks.Get("/events", h.WebhookEP.Events)
	webhooks.Get("/deliveries", h.WebhookEP.Deliveries)
	webhooks.Get("/", h.WebhookEP.List)
	webhooks.Post("/", h.WebhookEP.Create)
	webhooks.Patch("/:id", h.WebhookEP.Update)
	webhooks.Delete("/:id", h.WebhookEP.Delete)
	webhooks.Post("/:id/rotate", h.WebhookEP.Rotate)
	webhooks.Post("/:id/test", h.WebhookEP.TestFire)
}

// RegisterProgrammaticAPI wires the /api/v1/external/* token-authenticated
// surface. Each endpoint requires an API token whose scope set covers the
// operation. Rate limit defaults to 600/min (cfg.RateLimitWHM is reused so
// operators have a single dial to turn).
func RegisterProgrammaticAPI(app *fiber.App, cfg *config.Config, db *mongo.Database, tokens *services.APITokenService, h *DeveloperHandlers) {
	root := app.Group("/api/v1/external",
		middleware.APITokenAuth(tokens),
		middleware.APITokenRateLimit(cfg.RateLimitWHM),
	)

	// Domains
	domains := root.Group("/domains")
	domains.Get("/", middleware.RequireTokenScope("domain:read"), h.Programmatic.ListDomains)
	domains.Post("/", middleware.RequireTokenScope("domain:write"), h.Programmatic.CreateDomain)

	// SSL — keyed by domain to match the panel surface
	ssl := root.Group("/ssl")
	ssl.Post("/:domain/issue", middleware.RequireTokenScope("ssl:write"), h.Programmatic.IssueSSL)
	ssl.Post("/:domain/force", middleware.RequireTokenScope("ssl:write"), h.Programmatic.ForceSSL)

	// Email under each domain. The :domain segment scopes per-domain
	// operations and lets the API consumer omit "@<domain>" when posting
	// a mailbox short name.
	email := root.Group("/email/:domain")
	email.Get("/mailboxes", middleware.RequireTokenScope("email:read"), h.Programmatic.ListMailboxes)
	email.Post("/mailboxes", middleware.RequireTokenScope("email:write"), h.Programmatic.CreateMailbox)
	email.Get("/mailboxes/:addr/stats", middleware.RequireTokenScope("email:read"), h.Programmatic.GetMailboxStats)
	email.Delete("/mailboxes/:addr", middleware.RequireTokenScope("email:write"), h.Programmatic.DeleteMailbox)
	email.Post("/mailboxes/:addr/webmail-link", middleware.RequireTokenScope("email:webmail"), h.Programmatic.WebmailLink)
	email.Get("/forwarders", middleware.RequireTokenScope("email:read"), h.Programmatic.ListForwarders)
	email.Post("/forwarders", middleware.RequireTokenScope("email:write"), h.Programmatic.CreateForwarder)
	email.Delete("/forwarders/:id", middleware.RequireTokenScope("email:write"), h.Programmatic.DeleteForwarder)

	// Deploy Software — domain link/unlink on a service
	deploy := root.Group("/deploy/projects/:id/services/:svc")
	deploy.Post("/link-domain", middleware.RequireTokenScope("deploy:link"), h.Programmatic.LinkDomain)
	deploy.Delete("/link-domain/:domain", middleware.RequireTokenScope("deploy:link"), h.Programmatic.UnlinkDomain)
}
