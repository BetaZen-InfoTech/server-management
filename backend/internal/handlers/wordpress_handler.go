package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type WordPressHandler struct{ service *services.WordPressService }
func NewWordPressHandler(s *services.WordPressService) *WordPressHandler { return &WordPressHandler{service: s} }

func (h *WordPressHandler) List(c *fiber.Ctx) error {
	installs, err := h.service.List(c.Context()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, installs)
}
func (h *WordPressHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id"); wp, err := h.service.GetByID(c.Context(), id)
	if err != nil { return response.NotFound(c, "WordPress install not found") }
	return response.Success(c, wp)
}
func (h *WordPressHandler) CheckConflict(c *fiber.Ctx) error {
	domain := c.Query("domain"); path := c.Query("path")
	if domain == "" { return response.BadRequest(c, "domain is required", nil) }
	conflict, msg := h.service.CheckConflict(c.Context(), domain, path)
	return response.Success(c, fiber.Map{"conflict": conflict, "message": msg})
}
func (h *WordPressHandler) Install(c *fiber.Ctx) error {
	var req models.InstallWordPressRequest
	if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if errs := validator.Validate(req); errs != nil { return response.BadRequest(c, "Validation failed", errs) }
	wp, err := h.service.Install(c.Context(), &req); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Created(c, wp)
}
func (h *WordPressHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id"); if err := h.service.Delete(c.Context(), id); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "WordPress deleted", nil)
}
func (h *WordPressHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id"); if err := h.service.Update(c.Context(), id); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "WordPress updated", nil)
}
func (h *WordPressHandler) SecurityScan(c *fiber.Ctx) error {
	id := c.Params("id"); result, err := h.service.SecurityScan(c.Context(), id)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, result)
}
func (h *WordPressHandler) ListPlugins(c *fiber.Ctx) error {
	id := c.Params("id"); plugins, err := h.service.ListPlugins(c.Context(), id)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, plugins)
}
func (h *WordPressHandler) InstallPlugin(c *fiber.Ctx) error {
	id := c.Params("id"); var body struct{ Slug string `json:"slug"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.InstallPlugin(c.Context(), id, body.Slug); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Plugin installed", nil)
}
func (h *WordPressHandler) ToggleMaintenance(c *fiber.Ctx) error {
	id := c.Params("id"); var body struct{ Enabled bool `json:"enabled"` }
	if err := c.BodyParser(&body); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.ToggleMaintenance(c.Context(), id, body.Enabled); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Maintenance mode updated", nil)
}
