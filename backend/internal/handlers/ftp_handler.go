package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type FTPHandler struct {
	service *services.FTPService
}

func NewFTPHandler(s *services.FTPService) *FTPHandler {
	return &FTPHandler{service: s}
}

func (h *FTPHandler) List(c *fiber.Ctx) error {
	accounts, err := h.service.List(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, accounts)
}

func (h *FTPHandler) Create(c *fiber.Ctx) error {
	var req models.CreateFTPRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	acct, err := h.service.Create(c.UserContext(), &req)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Created(c, acct)
}

func (h *FTPHandler) ResetPassword(c *fiber.Ctx) error {
	id := c.Params("id")
	var req models.UpdateFTPPasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	acct, err := h.service.ResetPassword(c.UserContext(), id, req.Password)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, acct)
}

func (h *FTPHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.Delete(c.UserContext(), id); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "FTP account deleted", nil)
}
