package handlers

import (
	"strings"

	"github.com/betazeninfotech/mail-suite/internal/services"
	"github.com/gofiber/fiber/v2"
)

// BIMIHandler serves a sender domain's BIMI logo (the SVG referenced by that
// domain's default._bimi record) as a same-origin image the webmail can render.
// PUBLIC (no JWT) because it's loaded via an <img> tag; it only ever exposes a
// domain's already-public BIMI logo, and the fetch is SSRF-guarded + sanitized in
// the service.
type BIMIHandler struct {
	svc *services.BIMIService
}

func NewBIMIHandler(svc *services.BIMIService) *BIMIHandler {
	return &BIMIHandler{svc: svc}
}

// Logo serves GET /bimi/logo/:domain(.svg). 404 when the domain has no usable
// BIMI logo. The strict CSP + nosniff neutralize any script even though the SVG
// is served from our origin.
func (h *BIMIHandler) Logo(c *fiber.Ctx) error {
	domain := strings.TrimSuffix(strings.ToLower(c.Params("domain")), ".svg")
	svg, ok := h.svc.LogoBytes(c.UserContext(), domain)
	if !ok {
		return c.Status(fiber.StatusNotFound).SendString("no BIMI logo")
	}
	c.Set("Content-Type", "image/svg+xml; charset=utf-8")
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	c.Set("Cache-Control", "public, max-age=3600")
	return c.Send(svg)
}
