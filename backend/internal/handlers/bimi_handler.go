package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

// BIMIHandler serves the panel's mail logo publicly (for the BIMI `l=` URL) and
// exposes the owner-facing upload + per-domain publish controls.
type BIMIHandler struct {
	svc     *services.BIMIService
	domains *services.DomainService
}

func NewBIMIHandler(svc *services.BIMIService, domains *services.DomainService) *BIMIHandler {
	return &BIMIHandler{svc: svc, domains: domains}
}

// ServeLogo is the PUBLIC, unauthenticated endpoint mail clients fetch (the BIMI
// `l=` URL points here). It returns the stored SVG with a locked-down CSP +
// nosniff so that even though the SVG lives on the panel origin, it can't act as
// a script/XSS vector. 404 when no logo is set.
func (h *BIMIHandler) ServeLogo(c *fiber.Ctx) error {
	svg, ok := h.svc.GetLogoSVG(c.UserContext())
	if !ok {
		return c.Status(fiber.StatusNotFound).SendString("no mail logo set")
	}
	c.Set("Content-Type", "image/svg+xml; charset=utf-8")
	c.Set("X-Content-Type-Options", "nosniff")
	// Defense in depth: neutralize any script the sanitizer somehow missed.
	c.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	c.Set("Cache-Control", "public, max-age=3600")
	return c.SendString(svg)
}

// GetLogo returns whether a logo is set, its raw SVG (for preview), and the URL.
func (h *BIMIHandler) GetLogo(c *fiber.Ctx) error {
	svg, ok := h.svc.GetLogoSVG(c.UserContext())
	return response.Success(c, fiber.Map{
		"logo_set": ok,
		"svg":      svg,
		"logo_url": h.svc.LogoURL(),
	})
}

// UploadLogo validates + stores the mail-logo SVG. Body: { svg: "<raw svg>" }.
func (h *BIMIHandler) UploadLogo(c *fiber.Ctx) error {
	var body struct {
		SVG string `json:"svg"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	warnings, err := h.svc.SetLogo(c.UserContext(), []byte(body.SVG))
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Mail logo saved", fiber.Map{
		"logo_url": h.svc.LogoURL(),
		"warnings": warnings,
	})
}

// DeleteLogo clears the stored mail logo.
func (h *BIMIHandler) DeleteLogo(c *fiber.Ctx) error {
	if err := h.svc.DeleteLogo(c.UserContext()); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Mail logo removed", nil)
}

// GetVMC returns the configured VMC URL (needed for Gmail BIMI).
func (h *BIMIHandler) GetVMC(c *fiber.Ctx) error {
	return response.Success(c, fiber.Map{"vmc_url": h.svc.GetVMCURL(c.UserContext())})
}

// SetVMC sets (or clears, when empty) the VMC .pem URL. Body: { vmc_url }.
func (h *BIMIHandler) SetVMC(c *fiber.Ctx) error {
	var body struct {
		VMCURL string `json:"vmc_url"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	saved, err := h.svc.SetVMCURL(c.UserContext(), body.VMCURL)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "VMC updated", fiber.Map{"vmc_url": saved})
}

// NormalizeLogo re-applies the BIMI-PS auto-fix to the already-stored logo.
func (h *BIMIHandler) NormalizeLogo(c *fiber.Ctx) error {
	warnings, err := h.svc.NormalizeStoredLogo(c.UserContext())
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Mail logo normalized to BIMI SVG Tiny 1.2 PS", fiber.Map{"warnings": warnings})
}

// EnforceDMARC raises a domain's DMARC policy to quarantine (or reject) — the BIMI
// minimum. The caller must own the domain. Body: { policy?: "quarantine"|"reject" }.
func (h *BIMIHandler) EnforceDMARC(c *fiber.Ctx) error {
	d, err := h.domains.GetByID(c.UserContext(), c.Params("id"))
	if err != nil {
		return response.NotFound(c, "Domain not found")
	}
	var body struct {
		Policy string `json:"policy"`
	}
	_ = c.BodyParser(&body)
	rec, err := h.svc.EnforceDMARC(c.UserContext(), d.Domain, body.Policy)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	st, _ := h.svc.Status(c.UserContext(), d.Domain)
	return response.SuccessMessage(c, "DMARC enforced", fiber.Map{"dmarc_record": rec, "status": st})
}

// BIMIStatus returns the per-domain publication state (owner-owned domain).
func (h *BIMIHandler) BIMIStatus(c *fiber.Ctx) error {
	d, err := h.domains.GetByID(c.UserContext(), c.Params("id"))
	if err != nil {
		return response.NotFound(c, "Domain not found")
	}
	st, err := h.svc.Status(c.UserContext(), d.Domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, st)
}

// PublishBIMI publishes default._bimi.<domain> for a domain the caller owns.
func (h *BIMIHandler) PublishBIMI(c *fiber.Ctx) error {
	d, err := h.domains.GetByID(c.UserContext(), c.Params("id"))
	if err != nil {
		return response.NotFound(c, "Domain not found")
	}
	st, err := h.svc.PublishBIMI(c.UserContext(), d.Domain)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "BIMI record published", st)
}

// UnpublishBIMI removes the BIMI record for a domain the caller owns.
func (h *BIMIHandler) UnpublishBIMI(c *fiber.Ctx) error {
	d, err := h.domains.GetByID(c.UserContext(), c.Params("id"))
	if err != nil {
		return response.NotFound(c, "Domain not found")
	}
	st, err := h.svc.UnpublishBIMI(c.UserContext(), d.Domain)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "BIMI record removed", st)
}
