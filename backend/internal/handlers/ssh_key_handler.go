package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type SSHKeyHandler struct{ service *services.SSHKeyService }
func NewSSHKeyHandler(s *services.SSHKeyService) *SSHKeyHandler { return &SSHKeyHandler{service: s} }

func (h *SSHKeyHandler) List(c *fiber.Ctx) error {
	user := c.Params("user"); keys, err := h.service.List(c.Context(), user)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, keys)
}
func (h *SSHKeyHandler) Add(c *fiber.Ctx) error {
	user := c.Params("user"); var req models.AddSSHKeyRequest
	if err := c.BodyParser(&req); err != nil { return response.BadRequest(c, "Invalid request body", nil) }
	if errs := validator.Validate(req); errs != nil { return response.BadRequest(c, "Validation failed", errs) }
	key, err := h.service.Add(c.Context(), user, &req); if err != nil { return response.InternalError(c, err.Error()) }
	return response.Created(c, key)
}
func (h *SSHKeyHandler) Delete(c *fiber.Ctx) error {
	user := c.Params("user"); id := c.Params("id")
	if err := h.service.Delete(c.Context(), user, id); err != nil { return response.InternalError(c, err.Error()) }
	return response.SuccessMessage(c, "SSH key deleted", nil)
}
func (h *SSHKeyHandler) Generate(c *fiber.Ctx) error {
	user := c.Params("user"); result, err := h.service.Generate(c.Context(), user)
	if err != nil { return response.InternalError(c, err.Error()) }
	return response.Success(c, result)
}
