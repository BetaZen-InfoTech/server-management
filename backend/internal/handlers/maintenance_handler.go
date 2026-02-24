package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type MaintenanceHandler struct{ service *services.MaintenanceService }
func NewMaintenanceHandler(s *services.MaintenanceService) *MaintenanceHandler { return &MaintenanceHandler{service: s} }

func (h *MaintenanceHandler) Status(c *fiber.Ctx) error {
	status, err := h.service.GetStatus(c.Context()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, status)
}
func (h *MaintenanceHandler) EnableServer(c *fiber.Ctx) error {
	var req models.MaintenanceConfig; if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.EnableServer(c.Context(), &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Server maintenance mode enabled", nil)
}
func (h *MaintenanceHandler) DisableServer(c *fiber.Ctx) error {
	if err := h.service.DisableServer(c.Context()); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Server maintenance mode disabled", nil)
}
func (h *MaintenanceHandler) EnableDomain(c *fiber.Ctx) error {
	domain := c.Params("domain"); var req models.MaintenanceConfig
	if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.EnableDomain(c.Context(), domain, &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Domain maintenance mode enabled", nil)
}
func (h *MaintenanceHandler) DisableDomain(c *fiber.Ctx) error {
	domain := c.Params("domain")
	if err := h.service.DisableDomain(c.Context(), domain); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Domain maintenance mode disabled", nil)
}
