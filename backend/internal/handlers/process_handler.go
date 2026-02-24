package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type ProcessHandler struct{ service *services.ProcessService }
func NewProcessHandler(s *services.ProcessService) *ProcessHandler { return &ProcessHandler{service: s} }

func (h *ProcessHandler) List(c *fiber.Ctx) error {
	sort := c.Query("sort", "cpu"); limit := c.QueryInt("limit", 50)
	procs, err := h.service.List(c.Context(), sort, limit); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, procs)
}
func (h *ProcessHandler) Get(c *fiber.Ctx) error {
	pid := c.Params("pid"); proc, err := h.service.GetByPID(c.Context(), pid)
	if err != nil { return response.NotFound(c, "Process not found") }
	return response.Success(c, proc)
}
func (h *ProcessHandler) Kill(c *fiber.Ctx) error {
	pid := c.Params("pid"); var body struct{ Signal string `json:"signal"` }
	if err := c.BodyParser(&body); err != nil { body.Signal = "SIGTERM" }
	if err := h.service.Kill(c.Context(), pid, body.Signal); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Signal sent", nil)
}
func (h *ProcessHandler) ListServices(c *fiber.Ctx) error {
	svcs, err := h.service.ListServices(c.Context()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, svcs)
}
func (h *ProcessHandler) ControlService(c *fiber.Ctx) error {
	name := c.Params("name"); action := c.Params("action")
	if err := h.service.ControlService(c.Context(), name, action); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Service "+action+" completed", nil)
}
