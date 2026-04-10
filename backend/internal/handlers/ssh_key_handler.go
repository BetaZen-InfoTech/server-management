package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type SSHKeyHandler struct {
	service *services.SSHKeyService
	db      *mongo.Database
}

func NewSSHKeyHandler(s *services.SSHKeyService, db *mongo.Database) *SSHKeyHandler {
	return &SSHKeyHandler{service: s, db: db}
}

// WHM handlers (admin - specify user via param)

func (h *SSHKeyHandler) List(c *fiber.Ctx) error {
	user := c.Params("user")
	keys, err := h.service.List(c.Context(), user)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, keys)
}

func (h *SSHKeyHandler) Add(c *fiber.Ctx) error {
	user := c.Params("user")
	var req models.AddSSHKeyRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	key, err := h.service.Add(c.Context(), user, &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, key)
}

func (h *SSHKeyHandler) Delete(c *fiber.Ctx) error {
	user := c.Params("user")
	id := c.Params("id")
	if err := h.service.Delete(c.Context(), user, id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "SSH key deleted", nil)
}

func (h *SSHKeyHandler) Generate(c *fiber.Ctx) error {
	user := c.Params("user")
	result, err := h.service.Generate(c.Context(), user)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, result)
}

// cPanel handlers (customer - uses authenticated user's username)

func (h *SSHKeyHandler) getUsername(c *fiber.Ctx) (string, error) {
	userID := c.Locals("user_id").(string)
	oid, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return "", err
	}
	var user models.User
	err = h.db.Collection(database.ColUsers).FindOne(c.Context(), bson.M{"_id": oid}).Decode(&user)
	if err != nil {
		return "", err
	}
	return user.Username, nil
}

func (h *SSHKeyHandler) CPanelList(c *fiber.Ctx) error {
	username, err := h.getUsername(c)
	if err != nil {
		return response.InternalError(c, "Failed to resolve user")
	}
	keys, err := h.service.List(c.Context(), username)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, keys)
}

func (h *SSHKeyHandler) CPanelAdd(c *fiber.Ctx) error {
	username, err := h.getUsername(c)
	if err != nil {
		return response.InternalError(c, "Failed to resolve user")
	}
	var req models.AddSSHKeyRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	key, err := h.service.Add(c.Context(), username, &req)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, key)
}

func (h *SSHKeyHandler) CPanelDelete(c *fiber.Ctx) error {
	username, err := h.getUsername(c)
	if err != nil {
		return response.InternalError(c, "Failed to resolve user")
	}
	id := c.Params("id")
	if err := h.service.Delete(c.Context(), username, id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "SSH key deleted", nil)
}

func (h *SSHKeyHandler) CPanelGenerate(c *fiber.Ctx) error {
	username, err := h.getUsername(c)
	if err != nil {
		return response.InternalError(c, "Failed to resolve user")
	}
	result, err := h.service.Generate(c.Context(), username)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, result)
}
