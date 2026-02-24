package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type EmailHandler struct {
	service *services.EmailService
}

func NewEmailHandler(s *services.EmailService) *EmailHandler {
	return &EmailHandler{service: s}
}

func (h *EmailHandler) ListMailboxes(c *fiber.Ctx) error {
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	domain := c.Query("domain")
	mailboxes, total, err := h.service.ListMailboxes(c.Context(), domain, page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, mailboxes, page, limit, total)
}

func (h *EmailHandler) GetMailbox(c *fiber.Ctx) error {
	id := c.Params("id")
	m, err := h.service.GetMailbox(c.Context(), id)
	if err != nil {
		return response.NotFound(c, "Mailbox not found")
	}
	return response.Success(c, m)
}

func (h *EmailHandler) CreateMailbox(c *fiber.Ctx) error {
	var req models.CreateMailboxRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	m, err := h.service.CreateMailbox(c.Context(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, m)
}

func (h *EmailHandler) UpdateMailbox(c *fiber.Ctx) error {
	id := c.Params("id")
	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	m, err := h.service.UpdateMailbox(c.Context(), id, body)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, m)
}

func (h *EmailHandler) DeleteMailbox(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.DeleteMailbox(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Mailbox deleted", nil)
}

func (h *EmailHandler) ListForwarders(c *fiber.Ctx) error {
	domain := c.Query("domain")
	fwds, err := h.service.ListForwarders(c.Context(), domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, fwds)
}

func (h *EmailHandler) CreateForwarder(c *fiber.Ctx) error {
	var req models.EmailForwarder
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	f, err := h.service.CreateForwarder(c.Context(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, f)
}

func (h *EmailHandler) DeleteForwarder(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.DeleteForwarder(c.Context(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Forwarder deleted", nil)
}

func (h *EmailHandler) UpdateSpamSettings(c *fiber.Ctx) error {
	domain := c.Params("domain")
	var req models.SpamSettings
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	req.Domain = domain
	if err := h.service.UpdateSpamSettings(c.Context(), &req); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Spam settings updated", nil)
}

func (h *EmailHandler) SetupDKIM(c *fiber.Ctx) error {
	domain := c.Params("domain")
	result, err := h.service.SetupDKIM(c.Context(), domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, result)
}
