// Package routes — agent_routes.go mounts internal callbacks the on-host
// agents (Dovecot Sieve helper, future cron probes, etc.) hit on the panel.
// These routes are NOT JWT-protected: they're shared-secret-authenticated
// by the handler itself, since the bash/Python helpers that POST to them
// don't carry a user session. Token files live under /etc/serverpanel/ and
// are root-owned 0600 so the bearer never leaks to unprivileged users on
// the box.
package routes

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/handlers"
	"github.com/gofiber/fiber/v2"
)

// RegisterAgentRoutes wires the handful of unauthenticated-by-JWT endpoints
// the on-box helpers POST to. mail is optional (may be nil during early
// boot or in tests) — when nil, the route is silently skipped so a
// half-wired panel still boots.
func RegisterAgentRoutes(app *fiber.App, mail *handlers.MailIncomingHandler) {
	agent := app.Group("/api/v1/agent")

	agent.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	if mail != nil {
		// Bearer-auth via X-Mail-Hook-Token; the handler verifies against
		// /etc/serverpanel/mail-hook.token.
		agent.Post("/mail/incoming", mail.Receive)
	}
}
