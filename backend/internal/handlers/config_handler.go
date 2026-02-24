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
	cfg, err := h.service.GetAll(c.Context()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, cfg)
}
func (h *ConfigHandler) UpdateNginx(c *fiber.Ctx) error {
	var req models.NginxConfig; if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateNginx(c.Context(), &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Nginx config updated", nil)
}
func (h *ConfigHandler) UpdatePHP(c *fiber.Ctx) error {
	version := c.Params("version"); var req models.PHPConfig
	if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdatePHP(c.Context(), version, &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "PHP config updated", nil)
}
func (h *ConfigHandler) UpdateMongoDB(c *fiber.Ctx) error {
	var req models.MongoDBConfig; if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateMongoDB(c.Context(), &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "MongoDB config updated", nil)
}
func (h *ConfigHandler) UpdateHostname(c *fiber.Ctx) error {
	var body struct{ Hostname string `json:"hostname"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateHostname(c.Context(), body.Hostname); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Hostname updated", nil)
}
func (h *ConfigHandler) TestNginx(c *fiber.Ctx) error {
	result, err := h.service.TestNginx(c.Context()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, result)
}
func (h *ConfigHandler) RestartService(c *fiber.Ctx) error {
	service := c.Params("service")
	if err := h.service.RestartService(c.Context(), service); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, service+" restarted", nil)
}
