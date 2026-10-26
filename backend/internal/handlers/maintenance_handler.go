package handlers

import (
	"context"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type MaintenanceHandler struct{ service *services.MaintenanceService }

func NewMaintenanceHandler(s *services.MaintenanceService) *MaintenanceHandler {
	return &MaintenanceHandler{service: s}
}

func (h *MaintenanceHandler) Status(c *fiber.Ctx) error {
	status, err := h.service.GetStatus(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, status)
}
func (h *MaintenanceHandler) EnableServer(c *fiber.Ctx) error {
	var req models.MaintenanceConfig
	_ = c.BodyParser(&req) // body is optional — all fields have defaults
	if err := h.service.EnableServer(c.UserContext(), &req); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Server maintenance mode enabled", nil)
}
func (h *MaintenanceHandler) DisableServer(c *fiber.Ctx) error {
	if err := h.service.DisableServer(c.UserContext()); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Server maintenance mode disabled", nil)
}
func (h *MaintenanceHandler) EnableDomain(c *fiber.Ctx) error {
	domain := c.Params("domain")
	var req models.MaintenanceConfig
	_ = c.BodyParser(&req) // body is optional — all fields have defaults
	if err := h.service.EnableDomain(c.UserContext(), domain, &req); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Domain maintenance mode enabled", nil)
}
func (h *MaintenanceHandler) DisableDomain(c *fiber.Ctx) error {
	domain := c.Params("domain")
	if err := h.service.DisableDomain(c.UserContext(), domain); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Domain maintenance mode disabled", nil)
}

// HealUnitLimits raises the fd/task ceilings fleet-wide: systemd drop-ins on
// every sp-proj-*/sp-app-* unit (running ones are restarted) + an nginx -t
// guarded worker_rlimit_nofile. Same shared logic as `bzpanel heal-unit-limits`.
// Idempotent, non-destructive. Gated server.manage at the route.
func (h *MaintenanceHandler) HealUnitLimits(c *fiber.Ctx) error {
	// Optional body {"restart": true} opts in to restarting running units whose
	// effective limit is still low (interrupts those apps). Default: drop-in only.
	var req struct {
		Restart bool `json:"restart"`
	}
	_ = c.BodyParser(&req)
	// Detached from the request so a client disconnect can't abort a half-done restart sweep.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	r, uErr := agent.EnsureUnitLimits(ctx, req.Restart)
	if r == nil {
		r = &agent.UnitLimitsResult{}
	}
	patched, nErr := agent.EnsureNginxFDLimit(ctx)
	out := fiber.Map{
		"units_healed":    r.Healed,
		"restart":         req.Restart,
		"units_restarted": r.Restarted,
		"units_skipped":   r.Skipped,
		"units_failed":    r.Failed,
		"nginx_patched":   patched,
		"messages":        r.Messages,
	}
	if uErr != nil {
		out["unit_error"] = uErr.Error()
	}
	if nErr != nil {
		out["nginx_error"] = nErr.Error()
	}
	return response.SuccessMessage(c, "File-descriptor limits applied", out)
}
