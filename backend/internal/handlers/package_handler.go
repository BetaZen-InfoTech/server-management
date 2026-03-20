package handlers

import (
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type PackageHandler struct {
	service *services.PackageService
}

func NewPackageHandler(s *services.PackageService) *PackageHandler {
	return &PackageHandler{service: s}
}

func (h *PackageHandler) List(c *fiber.Ctx) error {
	search := c.Query("search")
	packages, err := h.service.List(c.Context(), search)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, packages)
}

func (h *PackageHandler) Get(c *fiber.Ctx) error {
	id := c.Params("id")
	pkg, err := h.service.GetByID(c.Context(), id)
	if err != nil {
		return response.NotFound(c, "Package not found")
	}
	return response.Success(c, pkg)
}

func (h *PackageHandler) Create(c *fiber.Ctx) error {
	var req models.CreatePackageRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	createdBy := c.Locals("user_id").(string)
	pkg, err := h.service.Create(c.Context(), &req, createdBy)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, pkg)
}

func (h *PackageHandler) Update(c *fiber.Ctx) error {
	id := c.Params("id")
	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	pkg, err := h.service.Update(c.Context(), id, body)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, pkg)
}

func (h *PackageHandler) Delete(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.Delete(c.Context(), id); err != nil {
		if strings.Contains(err.Error(), "active accounts") {
			return response.BadRequest(c, err.Error(), nil)
		}
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Package deleted", nil)
}
