package handlers

import (
	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/betazeninfotech/mail-suite/internal/services"
	"github.com/betazeninfotech/mail-suite/pkg/response"
	"github.com/betazeninfotech/mail-suite/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type WebPushHandler struct {
	svc *services.WebPushService
}

func NewWebPushHandler(svc *services.WebPushService) *WebPushHandler {
	return &WebPushHandler{svc: svc}
}

// VapidPublic returns the server's VAPID public key so the browser can
// subscribe (applicationServerKey). Also reports whether push is configured.
func (h *WebPushHandler) VapidPublic(c *fiber.Ctx) error {
	return response.OK(c, fiber.Map{
		"public_key": h.svc.VapidPublic(),
		"enabled":    h.svc.Enabled(),
	})
}

func (h *WebPushHandler) Subscribe(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	if !h.svc.Enabled() {
		return response.BadRequest(c, "push notifications are not configured on this server")
	}
	var req models.PushSubscribeRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := h.svc.Subscribe(c.UserContext(), uid, req, c.Get("User-Agent")); err != nil {
		return response.Internal(c, err.Error())
	}
	return response.OK(c, fiber.Map{"subscribed": true})
}

func (h *WebPushHandler) Unsubscribe(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	var req models.PushUnsubscribeRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := h.svc.Unsubscribe(c.UserContext(), uid, req.Endpoint); err != nil {
		return response.Internal(c, err.Error())
	}
	return response.OK(c, fiber.Map{"unsubscribed": true})
}

// Test sends an immediate notification to all of the caller's subscribed
// browsers — used by the "Enable notifications" flow to prove it works.
func (h *WebPushHandler) Test(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	delivered := h.svc.SendToUser(c.UserContext(), uid, services.PushPayload{
		Title: "Betazen Mail",
		Body:  "🔔 Notifications are on — you'll be alerted when new mail arrives.",
		URL:   "/mail/inbox",
		Tag:   "test",
	})
	return response.OK(c, fiber.Map{"sent_to": delivered})
}
