package handlers

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

type AuthHandler struct {
	service      *services.AuthService
	auditService *services.AuditService
}

func NewAuthHandler(s *services.AuthService) *AuthHandler {
	return &AuthHandler{service: s}
}

func (h *AuthHandler) SetAuditService(a *services.AuditService) {
	h.auditService = a
}

func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req models.LoginRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	result, err := h.service.Login(c.Context(), &req, c.IP())
	if err != nil {
		// Log failed login
		if h.auditService != nil {
			h.auditService.LogAction(c.Context(), "", req.Email, "", "login.failed", "auth", "", "Failed login attempt for "+req.Email, c.IP(), c.Get("User-Agent"), "failure", nil)
		}
		return response.Unauthorized(c, err.Error())
	}
	// Log successful login
	if h.auditService != nil {
		uid := result.User.ID.Hex()
		h.auditService.LogAction(c.Context(), uid, result.User.Email, result.User.Role, "login.success", "auth", uid, "User logged in", c.IP(), c.Get("User-Agent"), "success", nil)
	}
	return response.Success(c, result)
}

func (h *AuthHandler) Refresh(c *fiber.Ctx) error {
	var body struct {
		RefreshToken string `json:"refresh_token" validate:"required"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	result, err := h.service.RefreshToken(c.Context(), body.RefreshToken)
	if err != nil {
		return response.Unauthorized(c, err.Error())
	}
	return response.Success(c, result)
}

func (h *AuthHandler) Logout(c *fiber.Ctx) error {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	_ = h.service.Logout(c.Context(), body.RefreshToken)
	return response.SuccessMessage(c, "Logged out successfully", nil)
}

func (h *AuthHandler) ForgotPassword(c *fiber.Ctx) error {
	var body struct {
		Email string `json:"email" validate:"required,email"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	_ = h.service.ForgotPassword(c.Context(), body.Email)
	return response.SuccessMessage(c, "If that email exists, a reset link has been sent", nil)
}

func (h *AuthHandler) ResetPassword(c *fiber.Ctx) error {
	var body struct {
		Token       string `json:"token" validate:"required"`
		NewPassword string `json:"new_password" validate:"required,min=8"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.ResetPassword(c.Context(), body.Token, body.NewPassword); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Password has been reset", nil)
}

func (h *AuthHandler) Enable2FA(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	result, err := h.service.Enable2FA(c.Context(), userID)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, result)
}

func (h *AuthHandler) Verify2FA(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	var body struct {
		Code string `json:"code" validate:"required"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if err := h.service.Verify2FA(c.Context(), userID, body.Code); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "2FA has been activated", nil)
}

func (h *AuthHandler) Disable2FA(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	if err := h.service.Disable2FA(c.Context(), userID); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "2FA has been disabled", nil)
}
