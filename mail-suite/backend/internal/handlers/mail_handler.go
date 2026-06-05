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

type MailHandler struct {
	svc *services.MailService
}

func NewMailHandler(svc *services.MailService) *MailHandler {
	return &MailHandler{svc: svc}
}

func (h *MailHandler) Folders(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	aid, err := primitive.ObjectIDFromHex(c.Params("account_id"))
	if err != nil {
		return response.BadRequest(c, "invalid account_id")
	}
	fs, err := h.svc.Folders(c.UserContext(), uid, aid)
	if err != nil {
		return response.Internal(c, err.Error())
	}
	return response.OK(c, fs)
}

func (h *MailHandler) Threads(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	aid, err := primitive.ObjectIDFromHex(c.Params("account_id"))
	if err != nil {
		return response.BadRequest(c, "invalid account_id")
	}
	folder := c.Query("folder", "INBOX")
	page, _ := strconv.Atoi(c.Query("page", "1"))
	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	hs, total, err := h.svc.Headers(c.UserContext(), uid, aid, folder, page, limit)
	if err != nil {
		return response.Internal(c, err.Error())
	}
	return response.OK(c, fiber.Map{"items": hs, "total": total, "page": page, "limit": limit})
}

func (h *MailHandler) Message(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	aid, err := primitive.ObjectIDFromHex(c.Params("account_id"))
	if err != nil {
		return response.BadRequest(c, "invalid account_id")
	}
	folder := c.Query("folder", "INBOX")
	u, err := strconv.ParseUint(c.Params("uid"), 10, 32)
	if err != nil {
		return response.BadRequest(c, "invalid uid")
	}
	body, err := h.svc.Message(c.UserContext(), uid, aid, folder, uint32(u))
	if err != nil {
		return response.Internal(c, err.Error())
	}
	return response.OK(c, body)
}

func (h *MailHandler) Flag(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	aid, err := primitive.ObjectIDFromHex(c.Params("account_id"))
	if err != nil {
		return response.BadRequest(c, "invalid account_id")
	}
	folder := c.Query("folder", "INBOX")
	u, err := strconv.ParseUint(c.Params("uid"), 10, 32)
	if err != nil {
		return response.BadRequest(c, "invalid uid")
	}
	var req models.MessageFlagRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := h.svc.Flag(c.UserContext(), uid, aid, folder, uint32(u), req); err != nil {
		return response.Internal(c, err.Error())
	}
	return response.NoContent(c)
}

func (h *MailHandler) Send(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	aid, err := primitive.ObjectIDFromHex(c.Params("account_id"))
	if err != nil {
		return response.BadRequest(c, "invalid account_id")
	}
	var req models.SendRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := h.svc.Send(c.UserContext(), uid, aid, &req); err != nil {
		return response.Internal(c, err.Error())
	}
	return response.OK(c, fiber.Map{"sent": true})
}
