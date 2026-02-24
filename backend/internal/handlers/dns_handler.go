package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type DNSHandler struct {
	service *services.DNSService
}

func NewDNSHandler(s *services.DNSService) *DNSHandler {
	return &DNSHandler{service: s}
}

func (h *DNSHandler) ListZones(c *fiber.Ctx) error {
	zones, err := h.service.ListZones(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, zones)
}

func (h *DNSHandler) GetZone(c *fiber.Ctx) error {
	domain := c.Params("domain")
	zone, err := h.service.GetZone(c.Context(), domain)
	if err != nil {
		return response.NotFound(c, "Zone not found")
	}
	return response.Success(c, zone)
}

func (h *DNSHandler) CreateZone(c *fiber.Ctx) error {
	var req models.CreateZoneRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	zone, err := h.service.CreateZone(c.Context(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, zone)
}

func (h *DNSHandler) DeleteZone(c *fiber.Ctx) error {
	domain := c.Params("domain")
	if err := h.service.DeleteZone(c.Context(), domain); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Zone deleted", nil)
}

func (h *DNSHandler) ListRecords(c *fiber.Ctx) error {
	domain := c.Params("domain")
	records, err := h.service.ListRecords(c.Context(), domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, records)
}

func (h *DNSHandler) AddRecord(c *fiber.Ctx) error {
	domain := c.Params("domain")
	var req models.CreateRecordRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	record, err := h.service.AddRecord(c.Context(), domain, &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, record)
}

func (h *DNSHandler) UpdateRecord(c *fiber.Ctx) error {
	domain := c.Params("domain")
	id := c.Params("id")
	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	record, err := h.service.UpdateRecord(c.Context(), domain, id, body)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, record)
}

func (h *DNSHandler) DeleteRecord(c *fiber.Ctx) error {
	domain := c.Params("domain")
	id := c.Params("id")
	if err := h.service.DeleteRecord(c.Context(), domain, id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Record deleted", nil)
}

func (h *DNSHandler) ExportZone(c *fiber.Ctx) error {
	domain := c.Params("domain")
	data, err := h.service.ExportZone(c.Context(), domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	c.Set("Content-Type", "text/plain")
	return c.SendString(data)
}
