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
	action := c.Query("action"); resource := c.Query("resource"); userID := c.Query("user_id")
	since := c.Query("since"); until := c.Query("until")
	logs, total, err := h.service.List(c.Context(), page, limit, action, resource, userID, since, until)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Paginated(c, logs, page, limit, total)
}
func (h *AuditHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id"); entry, err := h.service.GetByID(c.Context(), id)
	if err != nil { return response.NotFound(c, "Audit log not found") }
	return response.Success(c, entry)
}
func (h *AuditHandler) Export(c *fiber.Ctx) error {
	format := c.Query("format", "json"); since := c.Query("since"); until := c.Query("until")
	data, contentType, err := h.service.Export(c.Context(), format, since, until)
	if err != nil { return response.InternalError(c, err.Error()) }
	c.Set("Content-Type", contentType)
	return c.Send(data)
}
