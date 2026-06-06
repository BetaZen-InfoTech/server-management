package handlers

import (
	"errors"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// MailSuiteHandler exposes admin endpoints for the separate mail-suite
// product. Mounted under /api/v1/whm/mail-suite/*; owner-only.
type MailSuiteHandler struct {
	svc *services.MailSuiteService
}

func NewMailSuiteHandler(svc *services.MailSuiteService) *MailSuiteHandler {
	return &MailSuiteHandler{svc: svc}
}

func (h *MailSuiteHandler) List(c *fiber.Ctx) error {
	out, err := h.svc.List(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, out)
}

func (h *MailSuiteHandler) Register(c *fiber.Ctx) error {
	var req models.RegisterMailSuiteRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	if errs := validator.Validate(req); len(errs) > 0 {
		return response.BadRequest(c, "validation failed", errs)
	}
	out, err := h.svc.Register(c.UserContext(), req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, out)
}

func (h *MailSuiteHandler) Delete(c *fiber.Ctx) error {
	oid, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "invalid id", nil)
	}
	if err := h.svc.Delete(c.UserContext(), oid); err != nil {
		return response.InternalError(c, err.Error())
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *MailSuiteHandler) EnableMail(c *fiber.Ctx) error {
	out, err := h.svc.EnableMail(c.UserContext(), c.Params("domain"))
	if err != nil {
		if errors.Is(err, services.ErrMailSuiteNotConfigured) {
			return response.NotFound(c, err.Error())
		}
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, out)
}

func (h *MailSuiteHandler) Status(c *fiber.Ctx) error {
	out, err := h.svc.Status(c.UserContext(), c.Params("domain"))
	if err != nil {
		if errors.Is(err, services.ErrMailSuiteNotConfigured) {
			return response.NotFound(c, err.Error())
		}
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, out)
}

func (h *MailSuiteHandler) Webmail(c *fiber.Ctx) error {
	u, err := h.svc.WebmailURL(c.UserContext())
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.Success(c, fiber.Map{"url": u})
}

type installRequest struct {
	Domain string `json:"domain" validate:"required,fqdn"`
	Label  string `json:"label"`
}

// Install is the one-click provisioner the WHM Mail Suite page calls.
// The operator types ONE thing — the public domain — and this:
//   1. Runs the agent install steps (binary, .env with auto-generated
//      JWT + service token, systemd, nginx, Let's Encrypt).
//   2. Auto-registers the resulting deployment in Mongo.
//
// No service token paste, no .env editing, no SSH session.
func (h *MailSuiteHandler) Install(c *fiber.Ctx) error {
	var req installRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	if errs := validator.Validate(req); len(errs) > 0 {
		return response.BadRequest(c, "validation failed", errs)
	}
	dep, err := h.svc.Install(c.UserContext(), req.Domain, req.Label)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, dep)
}
