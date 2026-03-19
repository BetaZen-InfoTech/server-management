package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type CronHandler struct{ service *services.CronService }
func NewCronHandler(s *services.CronService) *CronHandler { return &CronHandler{service: s} }

func (h *CronHandler) List(c *fiber.Ctx) error {
	domain := c.Query("domain"); jobs, err := h.service.List(c.Context(), domain, "")
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, jobs)
}
func (h *CronHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id"); job, err := h.service.GetByID(c.Context(), id)
	if err != nil { return response.NotFound(c, "Cron job not found") }
	return response.Success(c, job)
}
func (h *CronHandler) Create(c *fiber.Ctx) error {
	var req models.CreateCronRequest; if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if errs := validator.Validate(req); errs != nil { return response.BadRequest(c, "Validation failed", errs) }
	job, err := h.service.Create(c.Context(), &req); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Created(c, job)
}
func (h *CronHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id"); var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	job, err := h.service.Update(c.Context(), id, body); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, job)
}
func (h *CronHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id"); if err := h.service.Delete(c.Context(), id); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Cron job deleted", nil)
}
func (h *CronHandler) Toggle(c *fiber.Ctx) error {
	id := c.Params("id"); if err := h.service.Toggle(c.Context(), id); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Cron job toggled", nil)
}
func (h *CronHandler) RunNow(c *fiber.Ctx) error {
	id := c.Params("id"); result, err := h.service.RunNow(c.Context(), id)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, result)
}
func (h *CronHandler) History(c *fiber.Ctx) error {
	id := c.Params("id"); history, err := h.service.History(c.Context(), id)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, history)
}

// CPanelList returns cron jobs scoped to the authenticated cPanel user.
func (h *CronHandler) CPanelList(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	jobs, err := h.service.ListByUser(c.Context(), userID)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, jobs)
}

// CPanelCreate creates a cron job with user/domain auto-populated from the authenticated cPanel user.
func (h *CronHandler) CPanelCreate(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	var req models.CreateCronRequest
	if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	job, err := h.service.CPanelCreate(c.Context(), userID, &req)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Created(c, job)
}
