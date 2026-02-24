package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type ResourceHandler struct{ service *services.ResourceService }
func NewResourceHandler(s *services.ResourceService) *ResourceHandler { return &ResourceHandler{service: s} }

func (h *ResourceHandler) Summary(c *fiber.Ctx) error {
	data, err := h.service.Summary(c.Context()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, data)
}
func (h *ResourceHandler) DomainUsage(c *fiber.Ctx) error {
	domain := c.Params("domain"); data, err := h.service.DomainUsage(c.Context(), domain)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, data)
}
func (h *ResourceHandler) Bandwidth(c *fiber.Ctx) error {
	period := c.Query("period", ""); interval := c.Query("interval", "daily")
	data, err := h.service.Bandwidth(c.Context(), period, interval)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, data)
}
func (h *ResourceHandler) BandwidthByDomain(c *fiber.Ctx) error {
	domain := c.Params("domain"); data, err := h.service.BandwidthByDomain(c.Context(), domain)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, data)
}
func (h *ResourceHandler) UpdateLimits(c *fiber.Ctx) error {
	domain := c.Params("domain"); var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateLimits(c.Context(), domain, body); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Limits updated", nil)
}
