package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type SSLHandler struct {
	service *services.SSLService
}

func NewSSLHandler(s *services.SSLService) *SSLHandler {
	return &SSLHandler{service: s}
}

func (h *SSLHandler) List(c *fiber.Ctx) error {
	certs, err := h.service.List(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, certs)
}

func (h *SSLHandler) Get(c *fiber.Ctx) error {
	domain := c.Params("domain")
	cert, err := h.service.GetByDomain(c.UserContext(), domain)
	if err != nil {
		return response.NotFound(c, "Certificate not found")
	}
	return response.Success(c, cert)
}

func (h *SSLHandler) IssueLetsEncrypt(c *fiber.Ctx) error {
	var req models.IssueLetsEncryptRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	cert, err := h.service.IssueLetsEncrypt(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, cert)
}

// IssueLetsEncryptBulk handles the multi-domain SSL request fired by
// the SSL page's "Issue X Certificates" button. Returns 200 with the
// per-item response even on partial failure — the UI uses the
// success/failed counts to decide which toast to render. Only a
// validation error or a malformed body returns a non-2xx.
func (h *SSLHandler) IssueLetsEncryptBulk(c *fiber.Ctx) error {
	var req models.IssueLetsEncryptBulkRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	resp, err := h.service.IssueLetsEncryptBulk(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, resp)
}

func (h *SSLHandler) UploadCustom(c *fiber.Ctx) error {
	var req models.UploadCustomCertRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	cert, err := h.service.UploadCustom(c.UserContext(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, cert)
}

func (h *SSLHandler) Renew(c *fiber.Ctx) error {
	domain := c.Params("domain")
	cert, err := h.service.Renew(c.UserContext(), domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, cert)
}

// Reissue forces a fresh Let's Encrypt certificate for an existing
// domain — operator-initiated when they need a new cert NOW, not
// when the renewal cron next fires. Same response shape as Renew so
// the UI can pipe both buttons through the same toast logic.
//
// Errors land as 400 (not 500) when they're certbot-friendly messages
// — DNS-not-pointing-here, rate-limit-exceeded, etc. — so the
// frontend's tryExtractBuildError-style banner can show actionable
// text instead of a generic "Internal server error".
func (h *SSLHandler) Reissue(c *fiber.Ctx) error {
	domain := c.Params("domain")
	cert, err := h.service.Reissue(c.UserContext(), domain)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, cert)
}

func (h *SSLHandler) Revoke(c *fiber.Ctx) error {
	domain := c.Params("domain")
	if err := h.service.Revoke(c.UserContext(), domain); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Certificate revoked", nil)
}

func (h *SSLHandler) ForceSSL(c *fiber.Ctx) error {
	domain := c.Params("domain")
	var req struct {
		Enable bool `json:"enable"`
	}
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.ForceSSL(c.UserContext(), domain, req.Enable); err != nil {
		return response.InternalError(c, err.Error())
	}
	msg := "Force SSL enabled"
	if !req.Enable {
		msg = "Force SSL disabled"
	}
	return response.SuccessMessage(c, msg, nil)
}

func (h *SSLHandler) Delete(c *fiber.Ctx) error {
	domain := c.Params("domain")
	if err := h.service.Delete(c.UserContext(), domain); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Certificate deleted", nil)
}
