package handlers

import (
	"github.com/betazeninfotech/mail-suite/internal/services"
	"github.com/gofiber/fiber/v2"
)

// MailResetHandler is a thin, same-origin proxy in front of the Betazen panel's
// public webmail self-service endpoints (/api/v1/mail-reset/*): recovery-email
// set/get + forgot/reset password. The webmail talks only to the mail-suite
// (no CORS, no panel URL baked into the SPA); the mail-suite forwards to the
// panel, which owns the mailbox, its Dovecot password and the mailer.
type MailResetHandler struct {
	panel *services.BetazenPanelClient
}

func NewMailResetHandler(panel *services.BetazenPanelClient) *MailResetHandler {
	return &MailResetHandler{panel: panel}
}

// clientIP returns the browser's address to forward to the panel so ITS per-IP
// rate limiter throttles the real client, not this proxy host. nginx sets
// X-Forwarded-For; fall back to the peer when absent.
func clientIP(c *fiber.Ctx) string {
	if xff := c.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return c.IP()
}

func (h *MailResetHandler) forward(c *fiber.Ctx, subpath string) error {
	status, body, err := h.panel.ForwardMailReset(c.UserContext(), subpath, clientIP(c), c.Body())
	if err != nil {
		msg := "Password reset is temporarily unavailable. Please try again later."
		if err == services.ErrPanelNotConfigured {
			msg = "Password reset is not enabled on this server."
		}
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"success": false,
			"error":   fiber.Map{"message": msg},
		})
	}
	// Relay the panel's own status + JSON body verbatim (it already carries the
	// user-facing message in the panel's {success,error:{message}} shape).
	c.Set("Content-Type", "application/json")
	return c.Status(status).Send(body)
}

func (h *MailResetHandler) Forgot(c *fiber.Ctx) error      { return h.forward(c, "forgot") }
func (h *MailResetHandler) Reset(c *fiber.Ctx) error       { return h.forward(c, "reset") }
func (h *MailResetHandler) SetRecovery(c *fiber.Ctx) error { return h.forward(c, "recovery-email") }
func (h *MailResetHandler) GetRecovery(c *fiber.Ctx) error { return h.forward(c, "recovery-email/get") }
