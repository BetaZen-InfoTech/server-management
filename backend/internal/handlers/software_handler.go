package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type SoftwareHandler struct{ service *services.SoftwareService }
func NewSoftwareHandler(s *services.SoftwareService) *SoftwareHandler { return &SoftwareHandler{service: s} }

func (h *SoftwareHandler) ListInstalled(c *fiber.Ctx) error {
	sw, err := h.service.ListInstalled(c.Context()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, sw)
}
func (h *SoftwareHandler) Install(c *fiber.Ctx) error {
	var body struct{ Software string `json:"software"`; Version string `json:"version"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.Install(c.Context(), body.Software, body.Version); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, body.Software+" installed", nil)
}
func (h *SoftwareHandler) Uninstall(c *fiber.Ctx) error {
	var body struct{ Software string `json:"software"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.Uninstall(c.Context(), body.Software); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, body.Software+" uninstalled", nil)
}
func (h *SoftwareHandler) CheckUpdates(c *fiber.Ctx) error {
	updates, err := h.service.CheckUpdates(c.Context()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, updates)
}
func (h *SoftwareHandler) InstallEmail(c *fiber.Ctx) error {
	var body map[string]interface{}; if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	result, err := h.service.InstallEmailServer(c.Context(), body); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, result)
}
func (h *SoftwareHandler) EmailStatus(c *fiber.Ctx) error {
	status, err := h.service.EmailServerStatus(c.Context()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, status)
}
