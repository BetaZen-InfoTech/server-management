package handlers

import (
	"errors"

	"github.com/betazeninfotech/mail-suite/internal/middleware"
	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/betazeninfotech/mail-suite/internal/services"
	"github.com/betazeninfotech/mail-suite/pkg/response"
	"github.com/betazeninfotech/mail-suite/pkg/validator"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type AuthHandler struct {
	svc *services.AuthService
}

func NewAuthHandler(svc *services.AuthService) *AuthHandler {
	return &AuthHandler{svc: svc}
}

func (h *AuthHandler) Register(c *fiber.Ctx) error {
	var req models.RegisterRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	pair, err := h.svc.Register(c.UserContext(), req, c.Get("User-Agent"), c.IP())
	if err != nil {
		if errors.Is(err, services.ErrUserExists) {
			return response.Conflict(c, "user already exists")
		}
		return response.Internal(c, err.Error())
	}
	return response.Created(c, pair)
}

func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req models.LoginRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	pair, err := h.svc.Login(c.UserContext(), req, c.Get("User-Agent"), c.IP())
	if err != nil {
		if errors.Is(err, services.ErrInvalidLogin) {
			return response.Unauthorized(c, "invalid email or password")
		}
		return response.Internal(c, err.Error())
	}
	return response.OK(c, pair)
}

func (h *AuthHandler) Refresh(c *fiber.Ctx) error {
	var req models.RefreshRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	if err := validator.Struct(req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	pair, err := h.svc.Refresh(c.UserContext(), req.RefreshToken, c.Get("User-Agent"), c.IP())
	if err != nil {
		return response.Unauthorized(c, "invalid or expired refresh token")
	}
	return response.OK(c, pair)
}

func (h *AuthHandler) Logout(c *fiber.Ctx) error {
	var req models.RefreshRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, err.Error())
	}
	_ = h.svc.Logout(c.UserContext(), req.RefreshToken)
	return response.NoContent(c)
}

func (h *AuthHandler) Me(c *fiber.Ctx) error {
	uid, _ := c.Locals(middleware.CtxUserID).(string)
	oid, err := primitive.ObjectIDFromHex(uid)
	if err != nil {
		return response.Unauthorized(c, "invalid user")
	}
	u, err := h.svc.GetUser(c.UserContext(), oid)
	if err != nil {
		return response.NotFound(c, "user not found")
	}
	return response.OK(c, u)
}
