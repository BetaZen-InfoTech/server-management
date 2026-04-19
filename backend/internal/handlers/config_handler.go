package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type ConfigHandler struct{ service *services.ConfigService }
func NewConfigHandler(s *services.ConfigService) *ConfigHandler { return &ConfigHandler{service: s} }

func (h *ConfigHandler) Get(c *fiber.Ctx) error {
	cfg, err := h.service.GetAll(c.UserContext()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, cfg)
}
func (h *ConfigHandler) UpdateNginx(c *fiber.Ctx) error {
	var req models.NginxConfig; if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateNginx(c.UserContext(), &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Nginx config updated", nil)
}
func (h *ConfigHandler) UpdatePHP(c *fiber.Ctx) error {
	var req models.PHPConfig
	if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdatePHP(c.UserContext(), &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "PHP config updated", nil)
}
func (h *ConfigHandler) UpdateMongoDB(c *fiber.Ctx) error {
	var req models.MongoDBConfig; if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateMongoDB(c.UserContext(), &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "MongoDB config updated", nil)
}
func (h *ConfigHandler) UpdateHostname(c *fiber.Ctx) error {
	var body struct{ Hostname string `json:"hostname"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateHostname(c.UserContext(), body.Hostname); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Hostname updated", nil)
}
func (h *ConfigHandler) UpdateTimezone(c *fiber.Ctx) error {
	var body struct{ Timezone string `json:"timezone"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateTimezone(c.UserContext(), body.Timezone); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Timezone updated", nil)
}
func (h *ConfigHandler) UpdateContactEmail(c *fiber.Ctx) error {
	var body struct{ Email string `json:"email"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateContactEmail(c.UserContext(), body.Email); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Contact email updated", nil)
}
func (h *ConfigHandler) TestNginx(c *fiber.Ctx) error {
	result, err := h.service.TestNginx(c.UserContext()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, result)
}
func (h *ConfigHandler) RestartService(c *fiber.Ctx) error {
	service := c.Params("service")
	if err := h.service.RestartService(c.UserContext(), service); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, service+" restarted", nil)
}

// GetPanelDomain returns the current panel access domain, its SSL status
// and the server's public IP so the UI can render DNS instructions.
func (h *ConfigHandler) GetPanelDomain(c *fiber.Ctx) error {
	data, err := h.service.GetPanelDomain(c.UserContext())
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, data)
}

// UpdatePanelDomain connects a new custom domain to the panel UI and
// optionally issues a Let's Encrypt certificate for it.
func (h *ConfigHandler) UpdatePanelDomain(c *fiber.Ctx) error {
	var body struct {
		Domain   string `json:"domain"`
		IssueSSL bool   `json:"issue_ssl"`
		Email    string `json:"email"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	result, err := h.service.UpdatePanelDomain(c.UserContext(), body.Domain, body.IssueSSL, body.Email)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, result)
}
