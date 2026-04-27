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
	zones, err := h.service.ListZones(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, zones)
}

func (h *DNSHandler) GetZone(c *fiber.Ctx) error {
	domain := c.Params("domain")
	zone, err := h.service.GetZone(c.UserContext(), domain)
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
	zone, err := h.service.CreateZone(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, zone)
}

func (h *DNSHandler) DeleteZone(c *fiber.Ctx) error {
	domain := c.Params("domain")
	if err := h.service.DeleteZone(c.UserContext(), domain); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Zone deleted", nil)
}

func (h *DNSHandler) ListRecords(c *fiber.Ctx) error {
	domain := c.Params("domain")
	records, err := h.service.ListRecords(c.UserContext(), domain)
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
	record, err := h.service.AddRecord(c.UserContext(), domain, &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, record)
}

// BulkAddRecords handles POST /dns/zones/:domain/records/bulk. Returns
// 200 even on partial failure — the per-item success/failure detail is
// in the body so the UI can render inline errors against the pending
// rows that produced them. A 4xx only fires on a malformed payload or
// validation failure of the WHOLE batch.
func (h *DNSHandler) BulkAddRecords(c *fiber.Ctx) error {
	domain := c.Params("domain")
	var req models.BulkRecordsRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	resp, err := h.service.BulkAddRecords(c.UserContext(), domain, &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, resp)
}

func (h *DNSHandler) UpdateRecord(c *fiber.Ctx) error {
	domain := c.Params("domain")
	id := c.Params("id")
	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	record, err := h.service.UpdateRecord(c.UserContext(), domain, id, body)
	if err == nil {
		return response.Success(c, record)
	}
	// Fallback: a stale browser tab (loaded BEFORE heal-on-read backfilled
	// Mongo) sends the all-zeros ObjectID for any record that previously
	// only existed in PowerDNS. Resolve the row by current name+type and
	// retry — works for the common A/AAAA/CNAME case where the row still
	// exists under the original name.
	if name, ok := body["name"].(string); ok {
		rtype, _ := body["type"].(string)
		if name != "" && rtype != "" {
			if rec2, err2 := h.service.UpdateRecordByNameType(c.UserContext(), domain, name, rtype, body); err2 == nil {
				return response.Success(c, rec2)
			}
		}
	}
	return response.InternalError(c, err.Error())
}

func (h *DNSHandler) DeleteRecord(c *fiber.Ctx) error {
	domain := c.Params("domain")
	id := c.Params("id")

	if err := h.service.DeleteRecord(c.UserContext(), domain, id); err == nil {
		return response.SuccessMessage(c, "Record deleted", nil)
	} else if name := c.Query("name"); name != "" {
		// Stale-UI fallback: the row had no Mongo backing when the page
		// loaded, so the URL has the all-zeros ObjectID. Disambiguate
		// by the explicit ?name=&type=&value= the frontend can send.
		rtype := c.Query("type")
		val := c.Query("value")
		if rtype != "" {
			if err2 := h.service.DeleteRecordByNameType(c.UserContext(), domain, name, rtype, val); err2 != nil {
				return response.InternalError(c, err2.Error())
			}
			return response.SuccessMessage(c, "Record deleted", nil)
		}
		return response.InternalError(c, err.Error())
	} else {
		return response.InternalError(c, err.Error())
	}
}

func (h *DNSHandler) ExportZone(c *fiber.Ctx) error {
	domain := c.Params("domain")
	data, err := h.service.ExportZone(c.UserContext(), domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	c.Set("Content-Type", "text/plain")
	return c.SendString(data)
}

// ReconcileZone heals Mongo↔PowerDNS drift for one zone — collapses
// duplicate rows in Mongo and rewrites every rrset via replace-rrset
// so PowerDNS matches the surviving Mongo state. The response carries
// counts (rrsets written, duplicates merged) so the WHM UI can show a
// confirmation toast.
func (h *DNSHandler) ReconcileZone(c *fiber.Ctx) error {
	domain := c.Params("domain")
	report, err := h.service.ReconcileZone(c.UserContext(), domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, report)
}
