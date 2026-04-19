package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type DomainHandler struct {
	service *services.DomainService
}

func NewDomainHandler(s *services.DomainService) *DomainHandler {
	return &DomainHandler{service: s}
}

func (h *DomainHandler) List(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	search := c.Query("search")
	domains, total, err := h.service.List(c.UserContext(), page, limit, search)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, domains, page, limit, total)
}

func (h *DomainHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id")
	domain, err := h.service.GetByID(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, "Domain not found")
	}
	return response.Success(c, domain)
}

func (h *DomainHandler) Create(c *fiber.Ctx) error {
	var req models.CreateDomainRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	domain, err := h.service.Create(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, domain)
}

func (h *DomainHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")
	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	domain, err := h.service.Update(c.UserContext(), id, body)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, domain)
}

func (h *DomainHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if err := c.BodyParser(&body); err != nil || !body.Confirm {
		return response.BadRequest(c, "Confirmation required: set confirm=true", nil)
	}
	if err := h.service.Delete(c.UserContext(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Domain deleted", nil)
}

func (h *DomainHandler) Suspend(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.Suspend(c.UserContext(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Domain suspended", nil)
}

func (h *DomainHandler) Unsuspend(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.Unsuspend(c.UserContext(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Domain unsuspended", nil)
}

func (h *DomainHandler) SwitchPHP(c *fiber.Ctx) error {
	id := c.Params("id")
	var body struct {
		PHPVersion string `json:"php_version" validate:"required"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.SwitchPHP(c.UserContext(), id, body.PHPVersion); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "PHP version switched", nil)
}

func (h *DomainHandler) Stats(c *fiber.Ctx) error {
	id := c.Params("id")
	stats, err := h.service.GetStats(c.UserContext(), id)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, stats)
}

func (h *DomainHandler) ListOwn(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	domains, total, err := h.service.ListByUser(c.UserContext(), userID, page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, domains, page, limit, total)
}

// UpdateRegistration handles PATCH /domains/:id/registration — patches
// just the registrar / purchase / expiry / nameserver fields without
// touching anything else on the record. Separate modal = separate
// endpoint so accidental saves can't overwrite PHP version or quotas.
func (h *DomainHandler) UpdateRegistration(c *fiber.Ctx) error {
	id := c.Params("id")
	var req models.UpdateRegistrationRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body", nil)
	}
	domain, err := h.service.UpdateRegistration(c.UserContext(), id, &req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, domain)
}

// ExpiringSoon (GET /domains/expiring?days=30) feeds the dashboard
// widget. Defaults to 30 days when the query param is absent or zero.
// Returns domains sorted by nearest expiry, capped at 100 rows so a
// dashboard render never pulls the whole inventory.
func (h *DomainHandler) ExpiringSoon(c *fiber.Ctx) error {
	days := c.QueryInt("days", 30)
	domains, err := h.service.ExpiringSoon(c.UserContext(), days)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, domains)
}

// WhoisLookup (POST /domains/:id/whois-refresh) runs the `whois`
// binary against the stored domain name and returns the parsed
// fields. We return — but don't auto-save — the result so the
// operator can review and confirm before overwriting manually-
// entered purchase / expiry dates.
func (h *DomainHandler) WhoisLookup(c *fiber.Ctx) error {
	id := c.Params("id")
	// Load the domain so the caller doesn't have to pass the name
	// separately (avoids mismatch between URL id and payload domain).
	domain, err := h.service.GetByID(c.UserContext(), id)
	if err != nil {
		return response.NotFound(c, "domain not found")
	}
	result, err := h.service.WhoisLookup(c.UserContext(), domain.Domain)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, result)
}
