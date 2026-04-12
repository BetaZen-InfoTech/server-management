package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type NotificationHandler struct{ service *services.NotificationService }
func NewNotificationHandler(s *services.NotificationService) *NotificationHandler { return &NotificationHandler{service: s} }

func (h *NotificationHandler) GetSettings(c *fiber.Ctx) error {
	settings, err := h.service.GetSettings(c.UserContext()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, settings)
}
func (h *NotificationHandler) UpdateSettings(c *fiber.Ctx) error {
	var req models.NotificationSettings; if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if err := h.service.UpdateSettings(c.UserContext(), &req); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Settings updated", nil)
}
func (h *NotificationHandler) History(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1); limit := c.QueryInt("limit", 20)
	history, total, err := h.service.History(c.UserContext(), page, limit)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Paginated(c, history, page, limit, total)
}
func (h *NotificationHandler) ListWebhooks(c *fiber.Ctx) error {
	hooks, err := h.service.ListWebhooks(c.UserContext()); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, hooks)
}
func (h *NotificationHandler) CreateWebhook(c *fiber.Ctx) error {
	var req models.CreateWebhookRequest; if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	hook, err := h.service.CreateWebhook(c.UserContext(), &req); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Created(c, hook)
}
func (h *NotificationHandler) DeleteWebhook(c *fiber.Ctx) error {
	id := c.Params("id"); if err := h.service.DeleteWebhook(c.UserContext(), id); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Webhook deleted", nil)
}
func (h *NotificationHandler) TestWebhook(c *fiber.Ctx) error {
	id := c.Params("id"); if err := h.service.TestWebhook(c.UserContext(), id); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "Test webhook sent", nil)
}
