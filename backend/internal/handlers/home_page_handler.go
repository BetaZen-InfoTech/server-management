package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

// HomePageHandler exposes the public landing-page config.
//
// Two route surfaces:
//
//	GET  /api/v1/home-page              public, currently unused by the
//	                                    frontend (the page is rendered
//	                                    server-side at GET /), but
//	                                    exposed for parity with
//	                                    /api/v1/branding so a future
//	                                    SPA preview tab can consume it.
//	GET  /api/v1/whm/config/home-page   admin read, server.manage.
//	PUT  /api/v1/whm/config/home-page   admin write, server.manage.
//
// Like Branding, this is a per-install singleton — vendors can't override.
type HomePageHandler struct {
	service *services.HomePageService
}

func NewHomePageHandler(s *services.HomePageService) *HomePageHandler {
	return &HomePageHandler{service: s}
}

func (h *HomePageHandler) Get(c *fiber.Ctx) error {
	view, err := h.service.Get(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, view)
}

func (h *HomePageHandler) Save(c *fiber.Ctx) error {
	var req services.SaveHomePageRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body", nil)
	}
	view, err := h.service.Save(c.UserContext(), &req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, view)
}
