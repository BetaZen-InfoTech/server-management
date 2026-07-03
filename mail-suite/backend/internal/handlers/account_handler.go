package handlers

import (
	"github.com/betazeninfotech/mail-suite/internal/middleware"
	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/betazeninfotech/mail-suite/internal/services"
	"github.com/betazeninfotech/mail-suite/pkg/response"
	"github.com/betazeninfotech/mail-suite/pkg/validator"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type AccountHandler struct {
	svc *services.AccountService
}

func NewAccountHandler(svc *services.AccountService) *AccountHandler {
	return &AccountHandler{svc: svc}
}

func userOID(c *fiber.Ctx) (primitive.ObjectID, bool) {
	uid, _ := c.Locals(middleware.CtxUserID).(string)
	oid, err := primitive.ObjectIDFromHex(uid)
	if err != nil {
		return primitive.NilObjectID, false
	}
	return oid, true
}

func (h *AccountHandler) List(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	list, err := h.svc.List(c.UserContext(), uid)
	if err != nil {
		return response.Internal(c, err.Error())
	}
	return response.OK(c, list)
}

func (h *AccountHandler) Create(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	var req models.AddAccountRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	a, err := h.svc.Add(c.UserContext(), uid, req)
	if err != nil {
		return response.Internal(c, err.Error())
	}
	return response.Created(c, a)
}

func (h *AccountHandler) Test(c *fiber.Ctx) error {
	if _, ok := userOID(c); !ok {
		return response.Unauthorized(c, "invalid user")
	}
	var req models.TestAccountRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := h.svc.TestConnection(c.UserContext(), req); err != nil {
		// A failed test is expected user input (wrong password/host), not a
		// server fault — 400 with the leg (IMAP:/SMTP:) that failed.
		return response.BadRequest(c, err.Error())
	}
	return response.OK(c, fiber.Map{"ok": true})
}

func (h *AccountHandler) Delete(c *fiber.Ctx) error {
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

func (h *AccountHandler) SetPrimary(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	oid, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "invalid id")
	}
	if err := h.svc.SetPrimary(c.UserContext(), uid, oid); err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.NoContent(c)
}
