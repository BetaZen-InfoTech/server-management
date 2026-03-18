package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type DashboardHandler struct {
	service *services.DashboardService
}

func NewDashboardHandler(s *services.DashboardService) *DashboardHandler {
	return &DashboardHandler{service: s}
}

// WHMStats returns aggregate stats for the WHM admin dashboard.
// vendor_owner sees global stats; other roles see only their own resources.
func (h *DashboardHandler) WHMStats(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	role := c.Locals("role").(string)
	stats, err := h.service.GetWHMStats(c.Context(), userID, role)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, stats)
}

// WHMActivity returns recent audit log entries for the WHM dashboard.
// vendor_owner sees all activity; other roles see only their own.
func (h *DashboardHandler) WHMActivity(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	role := c.Locals("role").(string)
	activity, err := h.service.GetWHMActivity(c.Context(), userID, role)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, activity)
}

// WHMServerStatus returns live CPU, memory, disk, and uptime data.
func (h *DashboardHandler) WHMServerStatus(c *fiber.Ctx) error {
	status, err := h.service.GetServerStatus()
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, status)
}

// CPanelStats returns user-scoped stats for the cPanel dashboard.
func (h *DashboardHandler) CPanelStats(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	stats, err := h.service.GetCPanelStats(c.Context(), userID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, stats)
}

// CPanelActivity returns user-scoped recent activity for the cPanel dashboard.
func (h *DashboardHandler) CPanelActivity(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	activity, err := h.service.GetCPanelActivity(c.Context(), userID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, activity)
}
