package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
)

type ResourceHandler struct {
	service *services.ResourceService
	// userSvc lets the Vendors rollup reuse UserService.VendorStorageBytes
	// (which already caches `du -sb` per username for 5 min) instead of
	// running parallel disk walks. Optional — when nil, DiskBytes
	// reports 0 and the column renders "—".
	userSvc *services.UserService
}

func NewResourceHandler(s *services.ResourceService) *ResourceHandler {
	return &ResourceHandler{service: s}
}

// SetUserService wires in the user service so the per-vendor rollup can
// share the disk-usage cache. main.go calls this right after the
// handlers are constructed.
func (h *ResourceHandler) SetUserService(u *services.UserService) {
	h.userSvc = u
}

func (h *ResourceHandler) Summary(c *fiber.Ctx) error {
	data, err := h.service.Summary(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, data)
}

func (h *ResourceHandler) DomainUsage(c *fiber.Ctx) error {
	domain := c.Params("domain")
	data, err := h.service.DomainUsage(c.UserContext(), domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, data)
}

func (h *ResourceHandler) Bandwidth(c *fiber.Ctx) error {
	period := c.Query("period", "")
	interval := c.Query("interval", "daily")
	data, err := h.service.Bandwidth(c.UserContext(), period, interval)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, data)
}

func (h *ResourceHandler) BandwidthByDomain(c *fiber.Ctx) error {
	domain := c.Params("domain")
	data, err := h.service.BandwidthByDomain(c.UserContext(), domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, data)
}

func (h *ResourceHandler) UpdateLimits(c *fiber.Ctx) error {
	domain := c.Params("domain")
	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.UpdateLimits(c.UserContext(), domain, body); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Limits updated", nil)
}

// Vendors returns one rollup row per vendor for the Resources →
// Vendors tab. Each row has domain / app / db / mailbox / subdomain
// counts plus disk bytes, scoped to the vendor's tenant.
func (h *ResourceHandler) Vendors(c *fiber.Ctx) error {
	data, err := h.service.Vendors(c.UserContext(), h.userSvc)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, data)
}

// VendorDetail returns the full per-vendor breakdown — list of every
// domain / subdomain / app / database / mailbox / forwarder / cron job
// / FTP account owned by the vendor's tenant.
func (h *ResourceHandler) VendorDetail(c *fiber.Ctx) error {
	id := c.Params("id")
	data, err := h.service.VendorDetail(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.Success(c, data)
}
