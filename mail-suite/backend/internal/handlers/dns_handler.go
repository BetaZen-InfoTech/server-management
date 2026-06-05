package handlers

import (
	"errors"

	"github.com/betazeninfotech/mail-suite/internal/services"
	"github.com/betazeninfotech/mail-suite/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type DNSHandler struct {
	svc *services.DNSService
}

func NewDNSHandler(svc *services.DNSService) *DNSHandler {
	return &DNSHandler{svc: svc}
}

func (h *DNSHandler) EnableMail(c *fiber.Ctx) error {
	domain := c.Params("domain")
	st, err := h.svc.EnableMail(c.UserContext(), domain)
	if err != nil {
		if errors.Is(err, services.ErrPanelNotConfigured) {
			return response.Err(c, fiber.StatusServiceUnavailable, "panel_unconfigured",
				"Betazen panel API URL/token not configured — set BETAZEN_PANEL_URL and BETAZEN_PANEL_TOKEN")
		}
		return response.Internal(c, err.Error())
	}
	return response.OK(c, st)
}

func (h *DNSHandler) Status(c *fiber.Ctx) error {
	domain := c.Params("domain")
	st, err := h.svc.Verify(c.UserContext(), domain)
	if err != nil {
		return response.Internal(c, err.Error())
	}
	return response.OK(c, st)
}
