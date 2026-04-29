package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

// BrandingHandler exposes the panel's whitelabel config.
//
// Two route surfaces:
//
//	GET  /api/v1/branding              public, used by index.html /
//	                                   login page so they can render
//	                                   the configured logo + favicon
//	                                   BEFORE any auth token exists.
//	GET  /api/v1/whm/config/branding   admin, same data, gated on
//	                                   server.manage so the WHM
//	                                   Server Settings page can
//	                                   reuse it without changing
//	                                   the public endpoint.
//	PUT  /api/v1/whm/config/branding   admin write, server.manage.
//
// We deliberately don't expose a cpanel-side write — branding is
// per-install, not per-tenant, and a vendor can't override the
// platform-owner's logo.
type BrandingHandler struct {
	service *services.BrandingService
}

func NewBrandingHandler(s *services.BrandingService) *BrandingHandler {
	return &BrandingHandler{service: s}
}

func (h *BrandingHandler) Get(c *fiber.Ctx) error {
	view, err := h.service.Get(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, view)
}

func (h *BrandingHandler) Save(c *fiber.Ctx) error {
	var req services.SaveBrandingRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body", nil)
	}
	view, err := h.service.Save(c.UserContext(), &req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, view)
}
