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
	certs, err := h.service.List(c.Context())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, certs)
}

func (h *SSLHandler) Get(c *fiber.Ctx) error {
	domain := c.Params("domain")
	cert, err := h.service.GetByDomain(c.Context(), domain)
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
	cert, err := h.service.IssueLetsEncrypt(c.Context(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, cert)
}

func (h *SSLHandler) UploadCustom(c *fiber.Ctx) error {
	var req models.UploadCustomCertRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	cert, err := h.service.UploadCustom(c.Context(), &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, cert)
}

func (h *SSLHandler) Renew(c *fiber.Ctx) error {
	domain := c.Params("domain")
	cert, err := h.service.Renew(c.Context(), domain)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, cert)
}

func (h *SSLHandler) Revoke(c *fiber.Ctx) error {
	domain := c.Params("domain")
	if err := h.service.Revoke(c.Context(), domain); err != nil {
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
	if err := h.service.ForceSSL(c.Context(), domain, req.Enable); err != nil {
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
	if err := h.service.Delete(c.Context(), domain); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Certificate deleted", nil)
}
