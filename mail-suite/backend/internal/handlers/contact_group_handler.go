package handlers

import (
	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/betazeninfotech/mail-suite/internal/services"
	"github.com/betazeninfotech/mail-suite/pkg/response"
	"github.com/betazeninfotech/mail-suite/pkg/validator"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type ContactGroupHandler struct {
	svc *services.ContactGroupService
}

func NewContactGroupHandler(svc *services.ContactGroupService) *ContactGroupHandler {
	return &ContactGroupHandler{svc: svc}
}

func (h *ContactGroupHandler) List(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	items, err := h.svc.List(c.UserContext(), uid)
	if err != nil {
		return response.Internal(c, err.Error())
	}
	return response.OK(c, items)
}

func (h *ContactGroupHandler) Create(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	var req models.ContactGroupRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	g, err := h.svc.Create(c.UserContext(), uid, req)
	if err != nil {
		return response.Internal(c, err.Error())
	}
	return response.Created(c, g)
}

func (h *ContactGroupHandler) Update(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	oid, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "invalid id")
	}
	var req models.ContactGroupRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	g, err := h.svc.Update(c.UserContext(), uid, oid, req)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.OK(c, g)
}

func (h *ContactGroupHandler) Delete(c *fiber.Ctx) error {
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
