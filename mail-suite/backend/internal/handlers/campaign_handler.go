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

type CampaignHandler struct {
	svc *services.CampaignService
}

func NewCampaignHandler(svc *services.CampaignService) *CampaignHandler {
	return &CampaignHandler{svc: svc}
}

func (h *CampaignHandler) id(c *fiber.Ctx) (primitive.ObjectID, error) {
	return primitive.ObjectIDFromHex(c.Params("id"))
}

func (h *CampaignHandler) List(c *fiber.Ctx) error {
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

func (h *CampaignHandler) Get(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	oid, err := h.id(c)
	if err != nil {
		return response.BadRequest(c, "invalid id")
	}
	cm, err := h.svc.Get(c.UserContext(), uid, oid)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.OK(c, cm)
}

func (h *CampaignHandler) Create(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	var req models.CampaignRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	cm, err := h.svc.Create(c.UserContext(), uid, req)
	if err != nil {
		return response.BadRequest(c, err.Error())
	}
	return response.Created(c, cm)
}

func (h *CampaignHandler) Update(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	oid, err := h.id(c)
	if err != nil {
		return response.BadRequest(c, "invalid id")
	}
	var req models.CampaignRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	cm, err := h.svc.Update(c.UserContext(), uid, oid, req)
	if err != nil {
		if err == services.ErrCampaignState {
			return response.Conflict(c, err.Error())
		}
		return response.NotFound(c, err.Error())
	}
	return response.OK(c, cm)
}

func (h *CampaignHandler) Delete(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	oid, err := h.id(c)
	if err != nil {
		return response.BadRequest(c, "invalid id")
	}
	if err := h.svc.Delete(c.UserContext(), uid, oid); err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.NoContent(c)
}

// action runs a lifecycle transition (start/pause/cancel) and returns the fresh
// campaign.
func (h *CampaignHandler) action(c *fiber.Ctx, fn func(uid, id primitive.ObjectID) error) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	oid, err := h.id(c)
	if err != nil {
		return response.BadRequest(c, "invalid id")
	}
	if err := fn(uid, oid); err != nil {
		switch err {
		case services.ErrCampaignState, services.ErrNoRecipients:
			return response.BadRequest(c, err.Error())
		case services.ErrCampaignNotFound:
			return response.NotFound(c, err.Error())
		default:
			return response.Internal(c, err.Error())
		}
	}
	cm, err := h.svc.Get(c.UserContext(), uid, oid)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.OK(c, cm)
}

func (h *CampaignHandler) Start(c *fiber.Ctx) error {
	return h.action(c, func(uid, id primitive.ObjectID) error { return h.svc.Start(c.UserContext(), uid, id) })
}
func (h *CampaignHandler) Pause(c *fiber.Ctx) error {
	return h.action(c, func(uid, id primitive.ObjectID) error { return h.svc.Pause(c.UserContext(), uid, id) })
}
func (h *CampaignHandler) Cancel(c *fiber.Ctx) error {
	return h.action(c, func(uid, id primitive.ObjectID) error { return h.svc.Cancel(c.UserContext(), uid, id) })
}

func (h *CampaignHandler) Stats(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	oid, err := h.id(c)
	if err != nil {
		return response.BadRequest(c, "invalid id")
	}
	st, err := h.svc.Stats(c.UserContext(), uid, oid)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.OK(c, st)
}

// RecipientEvents returns one recipient's full open/click timeline.
func (h *CampaignHandler) RecipientEvents(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	cid, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, "invalid id")
	}
	rid, err := primitive.ObjectIDFromHex(c.Params("rid"))
	if err != nil {
		return response.BadRequest(c, "invalid recipient id")
	}
	events, err := h.svc.RecipientEvents(c.UserContext(), uid, cid, rid)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.OK(c, events)
}

func (h *CampaignHandler) Recipients(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	oid, err := h.id(c)
	if err != nil {
		return response.BadRequest(c, "invalid id")
	}
	page, _ := strconv.Atoi(c.Query("page", "1"))
	limit, _ := strconv.Atoi(c.Query("limit", "100"))
	items, err := h.svc.Recipients(c.UserContext(), uid, oid, page, limit)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.OK(c, items)
}
