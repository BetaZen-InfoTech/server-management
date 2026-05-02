// Package handlers — WebhookEndpointHandler is the JWT-authenticated surface
// for managing outbound webhook subscriptions. Distinct from WebhookHandler
// (which handles INBOUND GitHub push webhooks for Deploy Software).
package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type WebhookEndpointHandler struct {
	svc *services.WebhookService
}

func NewWebhookEndpointHandler(svc *services.WebhookService) *WebhookEndpointHandler {
	return &WebhookEndpointHandler{svc: svc}
}

func (h *WebhookEndpointHandler) List(c *fiber.Ctx) error {
	scope := services.GetCallerScope(c.UserContext())
	rows, err := h.svc.List(c.UserContext(), scope)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, rows)
}

// Events returns the static event catalogue used by the Create form.
func (h *WebhookEndpointHandler) Events(c *fiber.Ctx) error {
	return response.Success(c, models.AllWebhookEvents)
}

func (h *WebhookEndpointHandler) Create(c *fiber.Ctx) error {
	var req models.CreateWebhookEndpointRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	scope := services.GetCallerScope(c.UserContext())
	issued, err := h.svc.Subscribe(c.UserContext(), scope, req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Created(c, issued)
}

// Update accepts active / events / description / mailbox_email in the body.
// URL is immutable. mailbox_email is tri-state: omitted = unchanged, "" =
// clear binding (tenant-wide), "x@y.tld" = bind to that mailbox.
func (h *WebhookEndpointHandler) Update(c *fiber.Ctx) error {
	var body struct {
		Active       *bool    `json:"active"`
		Events       []string `json:"events"`
		Description  *string  `json:"description"`
		MailboxEmail *string  `json:"mailbox_email"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	scope := services.GetCallerScope(c.UserContext())
	if err := h.svc.Update(c.UserContext(), scope, c.Params("id"), body.Active, body.Events, body.Description, body.MailboxEmail); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Webhook updated", nil)
}

func (h *WebhookEndpointHandler) Delete(c *fiber.Ctx) error {
	scope := services.GetCallerScope(c.UserContext())
	if err := h.svc.Delete(c.UserContext(), scope, c.Params("id")); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Webhook deleted", nil)
}

func (h *WebhookEndpointHandler) Rotate(c *fiber.Ctx) error {
	scope := services.GetCallerScope(c.UserContext())
	issued, err := h.svc.Rotate(c.UserContext(), scope, c.Params("id"))
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, issued)
}

func (h *WebhookEndpointHandler) TestFire(c *fiber.Ctx) error {
	scope := services.GetCallerScope(c.UserContext())
	if err := h.svc.TestFire(c.UserContext(), scope, c.Params("id")); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Test event dispatched", nil)
}

func (h *WebhookEndpointHandler) Deliveries(c *fiber.Ctx) error {
	scope := services.GetCallerScope(c.UserContext())
	limit := c.QueryInt("limit", 100)
	rows, err := h.svc.ListDeliveries(c.UserContext(), scope, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, rows)
}
