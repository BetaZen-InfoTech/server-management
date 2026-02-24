package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type AuditHandler struct{ service *services.AuditService }
func NewAuditHandler(s *services.AuditService) *AuditHandler { return &AuditHandler{service: s} }

func (h *AuditHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1); limit := c.QueryInt("limit", 20)
	filters := map[string]string{}
	if v := c.Query("action"); v != "" { filters["action"] = v }
	if v := c.Query("resource"); v != "" { filters["resource"] = v }
	if v := c.Query("user_id"); v != "" { filters["user_id"] = v }
	if v := c.Query("since"); v != "" { filters["since"] = v }
	if v := c.Query("until"); v != "" { filters["until"] = v }
	logs, total, err := h.service.List(c.Context(), page, limit, filters)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Paginated(c, logs, page, limit, total)
}
func (h *AuditHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id"); entry, err := h.service.GetByID(c.Context(), id)
	if err != nil { return response.NotFound(c, "Audit log not found") }
	return response.Success(c, entry)
}
func (h *AuditHandler) Export(c *fiber.Ctx) error {
	format := c.Query("format", "json")
	filters := map[string]string{}
	if v := c.Query("since"); v != "" { filters["since"] = v }
	if v := c.Query("until"); v != "" { filters["until"] = v }
	path, err := h.service.Export(c.Context(), format, filters)
	if err != nil { return response.InternalError(c, err.Error()) }
	return c.Download(path)
}
