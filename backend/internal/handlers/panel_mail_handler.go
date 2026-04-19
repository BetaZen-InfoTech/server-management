package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

// PanelMailHandler exposes the panel's outgoing-mail (SMTP) configuration.
// Routes live under /api/v1/whm/config/mail and are gated on
// server.manage — only the platform operator should be editing who the
// panel sends mail as.
type PanelMailHandler struct {
	service *services.PanelMailService
}

func NewPanelMailHandler(s *services.PanelMailService) *PanelMailHandler {
	return &PanelMailHandler{service: s}
}

// Get returns the UI-safe view (password replaced with has_password
// boolean so the form can render "credentials saved" without ever
// echoing the plaintext password back to the browser).
func (h *PanelMailHandler) Get(c *fiber.Ctx) error {
	view, err := h.service.Get(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, view)
}

// Save upserts the config and reloads the shared Mailer so subsequent
// password-reset / notification mails use the new settings without a
// process restart.
func (h *PanelMailHandler) Save(c *fiber.Ctx) error {
	var req services.SavePanelMailRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	view, err := h.service.Save(c.UserContext(), &req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, view)
}

// Test fires a one-off "test email" to the address the operator supplied
// in the Server Settings card — the UI uses this for the "Send test"
// button so admins can verify SMTP before relying on it for password
// resets.
func (h *PanelMailHandler) Test(c *fiber.Ctx) error {
	var body struct {
		To string `json:"to"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.TestSend(c.UserContext(), body.To); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Test email sent — check the inbox in a few seconds", nil)
}
