package handlers

import (
	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/betazeninfotech/mail-suite/internal/services"
	"github.com/betazeninfotech/mail-suite/pkg/response"
	"github.com/betazeninfotech/mail-suite/pkg/validator"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type ForwarderHandler struct {
	svc *services.ForwarderService
}

func NewForwarderHandler(svc *services.ForwarderService) *ForwarderHandler {
	return &ForwarderHandler{svc: svc}
}

func (h *ForwarderHandler) List(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	out, err := h.svc.List(c.UserContext(), uid)
	if err != nil {
		return response.Internal(c, err.Error())
	}
	return response.OK(c, out)
}

func (h *ForwarderHandler) Create(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	var req models.ForwarderRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	out, err := h.svc.Create(c.UserContext(), uid, req)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}
	return response.Created(c, out)
}

func (h *ForwarderHandler) Delete(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	oid, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "invalid id")
	}
	if err := h.svc.Delete(c.UserContext(), uid, oid); err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.NoContent(c)
}
