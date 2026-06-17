// Package middleware — GuestAuth gates the no-login /api/v1/guest/* surface
// that the one-time magic link drives. Unlike Auth it reads a role="guest"
// JWT from an HttpOnly COOKIE (never the Authorization header), then reloads
// the backing guest_links row every request to re-check status, the 30-minute
// window, and the first-open browser binding — so a revoked/expired link or a
// second browser is rejected immediately. On success it pins a tenant-scoped
// CallerScope (the domain owner's) plus the session domain / link type onto
// the context for the guest handlers.
package middleware

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/jwt"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

func GuestAuth(cfg *config.Config, guest *services.GuestLinkService) fiber.Handler {
	return func(c *fiber.Ctx) error {
		sess := c.Cookies(services.GuestSessionCookieName)
		if sess == "" {
			return guestDenied(c)
		}
		claims, err := jwt.ValidateToken(cfg.JWTSecret, sess)
		if err != nil || claims.Role != "guest" {
			return guestDenied(c)
		}
		// claims.UserID carries the guest_links row id; the bind cookie is
		// the first-open browser token. LoadForSession re-checks both.
		link, err := guest.LoadForSession(c.UserContext(), claims.UserID, c.Cookies(services.GuestBindCookieName))
		if err != nil {
			return guestDenied(c)
		}

		// Pin the owning tenant's scope (tenant-scoped role ⇒ active
		// ownership enforcement) behind the handler-level domain forcing.
		c.SetUserContext(services.WithCallerScope(c.UserContext(), guest.Scope(link)))
		c.Locals("guest_link", link)
		c.Locals("guest_domain", link.Domain)
		c.Locals("guest_link_type", link.LinkType)
		return c.Next()
	}
}

// guestDenied clears the session cookies (each at the path it was set on) and
// returns 401 so a dead link stops retrying with stale cookies.
func guestDenied(c *fiber.Ctx) error {
	c.Cookie(&fiber.Cookie{Name: services.GuestSessionCookieName, Value: "", Path: "/api/v1/guest", MaxAge: -1, HTTPOnly: true, Secure: c.Secure(), SameSite: "Strict"})
	c.Cookie(&fiber.Cookie{Name: services.GuestBindCookieName, Value: "", Path: "/", MaxAge: -1, HTTPOnly: true, Secure: c.Secure(), SameSite: "Lax"})
	return response.Unauthorized(c, "This guest link has expired or was opened in another browser.")
}
