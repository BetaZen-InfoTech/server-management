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
	mailboxes, total, err := h.service.ListMailboxes(c.UserContext(), domain, page, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Paginated(c, mailboxes, page, limit, total)
}

func (h *EmailHandler) GetMailbox(c *fiber.Ctx) error {
	id := c.Params("id")
	m, err := h.service.GetMailbox(c.UserContext(), id)
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
	m, err := h.service.CreateMailbox(c.UserContext(), &req)
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
	m, err := h.service.UpdateMailbox(c.UserContext(), id, body)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, m)
}

func (h *EmailHandler) DeleteMailbox(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.DeleteMailbox(c.UserContext(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Mailbox deleted", nil)
}

func (h *EmailHandler) ListForwarders(c *fiber.Ctx) error {
	domain := c.Query("domain")
	fwds, err := h.service.ListForwarders(c.UserContext(), domain)
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
	f, err := h.service.CreateForwarder(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, f)
}

func (h *EmailHandler) DeleteForwarder(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.DeleteForwarder(c.UserContext(), id); err != nil {
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
	if err := h.service.UpdateSpamSettings(c.UserContext(), &req); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Spam settings updated", nil)
}

func (h *EmailHandler) SetupDKIM(c *fiber.Ctx) error {
	domain := c.Params("domain")
	result, err := h.service.SetupDKIM(c.UserContext(), domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, result)
}

func (h *EmailHandler) WebmailToken(c *fiber.Ctx) error {
	var req struct {
		Email string `json:"email" validate:"required,email"`
	}
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	token, err := h.service.GenerateWebmailToken(c.UserContext(), req.Email)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, map[string]string{
		"token": token,
		"url":   "/webmail/sso.php?token=" + token,
	})
}

// SendTest sends a test message from a panel-managed mailbox through
// local Postfix submission with SMTP-AUTH. Returns 200 with the full
// SMTP exchange trace on success, 422 with the same trace on failure —
// whichever side the break is on (auth, relay, DNS), the trace is in
// the response body so the operator can diagnose without digging
// through /var/log/mail.log.
func (h *EmailHandler) SendTest(c *fiber.Ctx) error {
	id := c.Params("id")
	var body struct {
		To string `json:"to" validate:"required,email"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(body); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	trace, err := h.service.SendTest(c.UserContext(), id, body.To)
	if err != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"success": false,
			"error": fiber.Map{
				"code":    "SMTP_TEST_FAILED",
				"message": err.Error(),
				"details": fiber.Map{"trace": trace},
			},
		})
	}
	return response.Success(c, fiber.Map{"trace": trace})
}

// ReconcileConfig rewrites the Dovecot + Postfix wiring on the VPS to
// match what EmailService expects. One-shot fix for servers installed
// with an earlier fragile sed-based setup; harmless to run on a
// working server since every step is idempotent. Platform-owner only.
func (h *EmailHandler) ReconcileConfig(c *fiber.Ctx) error {
	log, err := h.service.ReconcileConfig(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, fiber.Map{"log": log})
}
