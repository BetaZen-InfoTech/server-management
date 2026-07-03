package handlers

import (
	"strconv"

	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/betazeninfotech/mail-suite/internal/services"
	"github.com/betazeninfotech/mail-suite/pkg/response"
	"github.com/betazeninfotech/mail-suite/pkg/validator"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type ContactHandler struct {
	svc *services.ContactService
}

func NewContactHandler(svc *services.ContactService) *ContactHandler {
	return &ContactHandler{svc: svc}
}

func (h *ContactHandler) List(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	var groupID *primitive.ObjectID
	if q := c.Query("group_id"); q != "" {
		if oid, err := primitive.ObjectIDFromHex(q); err == nil {
			groupID = &oid
		}
	}
	page, _ := strconv.Atoi(c.Query("page", "1"))
	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	items, total, err := h.svc.List(c.UserContext(), uid, groupID, c.Query("status"), c.Query("search"), page, limit)
	if err != nil {
		return response.Internal(c, err.Error())
	}
	return response.OK(c, fiber.Map{"items": items, "total": total, "page": page, "limit": limit})
}

func (h *ContactHandler) Get(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	oid, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "invalid id")
	}
	ct, err := h.svc.Get(c.UserContext(), uid, oid)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.OK(c, ct)
}

func (h *ContactHandler) Create(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	var req models.ContactRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	ct, err := h.svc.Create(c.UserContext(), uid, req)
	if err != nil {
		if err == services.ErrContactExists {
			return response.Conflict(c, err.Error())
		}
		return response.Internal(c, err.Error())
	}
	return response.Created(c, ct)
}

func (h *ContactHandler) Update(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	oid, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "invalid id")
	}
	var req models.ContactRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	ct, err := h.svc.Update(c.UserContext(), uid, oid, req)
	if err != nil {
		if err == services.ErrContactExists {
			return response.Conflict(c, err.Error())
		}
		if err == services.ErrContactNotFound {
			return response.NotFound(c, err.Error())
		}
		return response.Internal(c, err.Error())
	}
	return response.OK(c, ct)
}

func (h *ContactHandler) Delete(c *fiber.Ctx) error {
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

func (h *ContactHandler) Import(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	var req models.ContactImportRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	res, err := h.svc.Import(c.UserContext(), uid, req)
	if err != nil {
		return response.Internal(c, err.Error())
	}
	return response.OK(c, res)
}
