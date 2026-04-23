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
	result, err := h.service.LoginWithUA(c.UserContext(), &req, c.IP(), c.Get("User-Agent"))
	if err != nil {
		// Log failed login
		if h.auditService != nil {
			h.auditService.LogAction(c.UserContext(), "", req.Email, "", "login.failed", "auth", "", "Failed login attempt for "+req.Email, c.IP(), c.Get("User-Agent"), "failure", nil)
		}
		return response.Unauthorized(c, err.Error())
	}
	// Log successful login
	if h.auditService != nil {
		uid := result.User.ID.Hex()
		h.auditService.LogAction(c.UserContext(), uid, result.User.Email, result.User.Role, "login.success", "auth", uid, "User logged in", c.IP(), c.Get("User-Agent"), "success", nil)
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
	result, err := h.service.RefreshToken(c.UserContext(), body.RefreshToken)
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
	_ = h.service.Logout(c.UserContext(), body.RefreshToken)
	return response.SuccessMessage(c, "Logged out successfully", nil)
}

func (h *AuthHandler) ForgotPassword(c *fiber.Ctx) error {
	var body struct {
		Email string `json:"email" validate:"required,email"`
		// Surface tells the service which panel to point the reset
		// link at: "whm" or "user-panel". Optional — when empty the
		// service falls back to picking by user role. The frontend's
		// public ForgotPasswordPage on each panel sends its own
		// surface so vendors don't get bounced to /whm/login by an
		// owner-shaped link.
		Surface string `json:"surface"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	// Discard the error on purpose — the public response is always
	// "if that email exists, we sent a link" so an attacker can't
	// enumerate registered emails. The service logs SMTP / DB
	// failures via zerolog so the operator still sees them.
	_ = h.service.ForgotPassword(c.UserContext(), body.Email, body.Surface)
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
	if err := h.service.ResetPassword(c.UserContext(), body.Token, body.NewPassword); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Password has been reset", nil)
}

// Me returns the signed-in user's profile so the WHM/cPanel Profile
// page can render the form.
func (h *AuthHandler) Me(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	if userID == "" {
		return response.Unauthorized(c, "missing user id in token")
	}
	u, err := h.service.Me(c.UserContext(), userID)
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.Success(c, fiber.Map{
		"id":       u.ID.Hex(),
		"username": u.Username,
		"name":     u.Name,
		"email":    u.Email,
		"role":     u.Role,
	})
}

// UpdateMe lets the signed-in user change their own name / email.
func (h *AuthHandler) UpdateMe(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	if userID == "" {
		return response.Unauthorized(c, "missing user id in token")
	}
	var body struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	u, err := h.service.UpdateProfile(c.UserContext(), userID, body.Name, body.Email)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, fiber.Map{
		"id":       u.ID.Hex(),
		"username": u.Username,
		"name":     u.Name,
		"email":    u.Email,
		"role":     u.Role,
	})
}

// ChangeMyPassword verifies the current password and rotates it to the new
// value. Other active sessions are invalidated (refresh token cleared).
func (h *AuthHandler) ChangeMyPassword(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	if userID == "" {
		return response.Unauthorized(c, "missing user id in token")
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if body.CurrentPassword == "" || body.NewPassword == "" {
		return response.BadRequest(c, "Both current_password and new_password are required", nil)
	}
	if err := h.service.ChangePassword(c.UserContext(), userID, body.CurrentPassword, body.NewPassword); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Password updated", nil)
}

func (h *AuthHandler) Enable2FA(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	result, err := h.service.Enable2FA(c.UserContext(), userID)
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
	if err := h.service.Verify2FA(c.UserContext(), userID, body.Code); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "2FA has been activated", nil)
}

func (h *AuthHandler) Disable2FA(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(string)
	if err := h.service.Disable2FA(c.UserContext(), userID); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "2FA has been disabled", nil)
}

// RequestOTP emails the caller a one-time login code. The response is
// always "if that email exists, we sent a code" — same enumeration-
// resistance story as ForgotPassword.
//
// `surface` ("whm" or "user-panel") controls the magic-link target so
// a vendor_owner and a customer both end up on the right SPA.
func (h *AuthHandler) RequestOTP(c *fiber.Ctx) error {
	var body struct {
		Email   string `json:"email" validate:"required,email"`
		Surface string `json:"surface"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(body); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	// Errors are swallowed on purpose — SMTP/DB failures are logged
	// inside the service so an operator can see them, but the public
	// response is always success-shaped.
	_ = h.service.RequestOTP(c.UserContext(), body.Email, body.Surface, c.IP(), c.Get("User-Agent"))
	if h.auditService != nil {
		h.auditService.LogAction(c.UserContext(), "", body.Email, "", "otp.request", "auth", "", "OTP login code requested", c.IP(), c.Get("User-Agent"), "success", nil)
	}
	return response.SuccessMessage(c, "If that email exists, a login code has been sent", nil)
}

// VerifyOTP validates a pending OTP and, on success, issues the same
// access/refresh pair as password login.
func (h *AuthHandler) VerifyOTP(c *fiber.Ctx) error {
	var body struct {
		Email string `json:"email" validate:"required,email"`
		Code  string `json:"code" validate:"required"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(body); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	result, err := h.service.VerifyOTP(c.UserContext(), body.Email, body.Code, c.IP(), c.Get("User-Agent"))
	if err != nil {
		if h.auditService != nil {
			h.auditService.LogAction(c.UserContext(), "", body.Email, "", "otp.verify.failed", "auth", "", "Invalid OTP for "+body.Email, c.IP(), c.Get("User-Agent"), "failure", nil)
		}
		return response.Unauthorized(c, err.Error())
	}
	if h.auditService != nil {
		uid := result.User.ID.Hex()
		h.auditService.LogAction(c.UserContext(), uid, result.User.Email, result.User.Role, "otp.verify.success", "auth", uid, "User logged in via OTP", c.IP(), c.Get("User-Agent"), "success", nil)
	}
	return response.Success(c, result)
}

// ListMySessions returns the signed-in user's recent logins for the
// Account → Sessions page. Hard cap is 200.
func (h *AuthHandler) ListMySessions(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(string)
	if userID == "" {
		return response.Unauthorized(c, "missing user id in token")
	}
	limit := c.QueryInt("limit", 50)
	sessions, err := h.service.ListSessions(c.UserContext(), userID, limit)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, sessions)
}
