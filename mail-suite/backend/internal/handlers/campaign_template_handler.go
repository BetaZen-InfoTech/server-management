package handlers

import (
	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/betazeninfotech/mail-suite/internal/services"
	"github.com/betazeninfotech/mail-suite/pkg/response"
	"github.com/betazeninfotech/mail-suite/pkg/validator"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type CampaignTemplateHandler struct {
	svc *services.CampaignTemplateService
}

func NewCampaignTemplateHandler(svc *services.CampaignTemplateService) *CampaignTemplateHandler {
	return &CampaignTemplateHandler{svc: svc}
}

func (h *CampaignTemplateHandler) List(c *fiber.Ctx) error {
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

func (h *CampaignTemplateHandler) Create(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	var req models.CampaignTemplateRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	t, err := h.svc.Create(c.UserContext(), uid, req)
	if err != nil {
		return response.Internal(c, err.Error())
	}
	return response.Created(c, t)
}

func (h *CampaignTemplateHandler) Delete(c *fiber.Ctx) error {
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
