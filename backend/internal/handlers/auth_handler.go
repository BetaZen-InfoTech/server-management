package handlers

import (
	"errors"

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
	// Service returns the raw browser-binding token so the handler
	// can stamp it on a cookie. Errors are swallowed on purpose —
	// SMTP/DB failures are logged inside the service but the public
	// response stays success-shaped for enumeration resistance.
	bindingToken, _ := h.service.RequestOTP(c.UserContext(), body.Email, body.Surface, c.IP(), c.Get("User-Agent"))
	if bindingToken != "" {
		// HTTP-only cookie pinned to the requesting browser. The
		// magic URL emailed alongside the code carries no token of
		// its own — verify will reject any browser that doesn't send
		// THIS cookie back, which is exactly the "URL pasted into
		// another browser → instant login" hole the user reported.
		//
		// SameSite=Lax allows the cookie on top-level link clicks
		// (the normal "click magic link in email client opens new
		// tab" flow) without exposing it to cross-site form posts.
		// Path narrowed to /api/v1/auth so it never piggybacks on
		// unrelated XHRs. Secure flag follows c.Secure() so HTTP
		// dev installs still set the cookie.
		c.Cookie(&fiber.Cookie{
			Name:     services.OTPBindingCookieName,
			Value:    bindingToken,
			Path:     "/api/v1/auth",
			MaxAge:   600, // 10 min — same as otpTTL
			HTTPOnly: true,
			Secure:   c.Secure(),
			SameSite: "Lax",
		})
	}
	if h.auditService != nil {
		h.auditService.LogAction(c.UserContext(), "", body.Email, "", "otp.request", "auth", "", "OTP login code requested", c.IP(), c.Get("User-Agent"), "success", nil)
	}
	return response.SuccessMessage(c, "If that email exists, a login code has been sent", nil)
}

// VerifyOTP validates a pending OTP and, on success, issues the same
// access/refresh pair as password login. Three response shapes:
//
//   - 200 with tokens → code + binding cookie both match the
//     originating browser; normal sign-in.
//   - 200 with {status:"approved_in_other_browser"} → code is
//     correct but the cookie doesn't match (the magic URL was
//     opened in a browser other than the one that requested it).
//     We've recorded a handoff approval; the originating browser
//     is polling /auth/otp/poll and will call /auth/otp/complete
//     to finish. The clicking browser gets no session.
//   - 401 with "invalid or expired code" → wrong / expired /
//     exhausted code. Attempts counter bumped.
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
	bindingToken := c.Cookies(services.OTPBindingCookieName)
	result, err := h.service.VerifyOTP(c.UserContext(), body.Email, body.Code, bindingToken, c.IP(), c.Get("User-Agent"))
	if err != nil {
		// Cross-browser click: the clicking browser has the right
		// code but no cookie. Service has already stamped the handoff
		// approval so the originating browser's poll picks it up. We
		// return 200 here so the frontend can show a friendly
		// "approved — return to your other browser" screen rather
		// than an error toast.
		if errors.Is(err, services.ErrOTPHandoffApproved) {
			if h.auditService != nil {
				h.auditService.LogAction(c.UserContext(), "", body.Email, "", "otp.handoff.approved", "auth", "", "OTP magic link approved from a different browser — awaiting completion on originating browser", c.IP(), c.Get("User-Agent"), "success", nil)
			}
			return response.Success(c, fiber.Map{
				"status":  "approved_in_other_browser",
				"message": err.Error(),
			})
		}
		if h.auditService != nil {
			h.auditService.LogAction(c.UserContext(), "", body.Email, "", "otp.verify.failed", "auth", "", "Invalid OTP for "+body.Email, c.IP(), c.Get("User-Agent"), "failure", nil)
		}
		return response.Unauthorized(c, err.Error())
	}
	// Success — clear the binding cookie so a stolen-cookie + stolen-
	// session-after-verify can't be reused. The Bearer access/refresh
	// tokens take over from here.
	c.Cookie(&fiber.Cookie{
		Name:     services.OTPBindingCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		MaxAge:   -1,
		HTTPOnly: true,
		Secure:   c.Secure(),
		SameSite: "Lax",
	})
	if h.auditService != nil {
		uid := result.User.ID.Hex()
		h.auditService.LogAction(c.UserContext(), uid, result.User.Email, result.User.Role, "otp.verify.success", "auth", uid, "User logged in via OTP", c.IP(), c.Get("User-Agent"), "success", nil)
	}
	return response.Success(c, result)
}

// PollOTP is the originating browser's way to learn when a magic-
// link click has arrived from another browser. Sits on the verify
// page, polls every few seconds, and when {handoff_approved:true}
// flips it calls /auth/otp/complete to finish the login.
//
// Always 200 — an absent / expired / cancelled OTP returns
// {pending:false} so the frontend stops polling without generating
// error noise. Auth is pure cookie-based; no JWT involved.
func (h *AuthHandler) PollOTP(c *fiber.Ctx) error {
	bindingToken := c.Cookies(services.OTPBindingCookieName)
	status, err := h.service.PollOTP(c.UserContext(), bindingToken)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, status)
}

// CompleteOTP finishes a handoff-approved sign-in. Only the
// originating browser (the one with the bz_otp_bind cookie that
// matches the stored binding hash) can trade its cookie for a
// session — an attacker who triggered the approval by clicking a
// forwarded magic URL doesn't have the cookie and gets a 401.
// Successful completion clears the binding cookie just like a
// direct VerifyOTP would.
func (h *AuthHandler) CompleteOTP(c *fiber.Ctx) error {
	bindingToken := c.Cookies(services.OTPBindingCookieName)
	result, err := h.service.CompleteOTP(c.UserContext(), bindingToken, c.IP(), c.Get("User-Agent"))
	if err != nil {
		if h.auditService != nil {
			h.auditService.LogAction(c.UserContext(), "", "", "", "otp.complete.failed", "auth", "", "OTP handoff completion rejected: "+err.Error(), c.IP(), c.Get("User-Agent"), "failure", nil)
		}
		return response.Unauthorized(c, err.Error())
	}
	c.Cookie(&fiber.Cookie{
		Name:     services.OTPBindingCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		MaxAge:   -1,
		HTTPOnly: true,
		Secure:   c.Secure(),
		SameSite: "Lax",
	})
	if h.auditService != nil {
		uid := result.User.ID.Hex()
		h.auditService.LogAction(c.UserContext(), uid, result.User.Email, result.User.Role, "otp.complete.success", "auth", uid, "User logged in via OTP (cross-browser handoff)", c.IP(), c.Get("User-Agent"), "success", nil)
	}
	return response.Success(c, result)
}

// CancelOTP revokes any pending OTP for the given email so the magic
// URL + code emailed earlier can no longer be redeemed. Powers the
// "Cancel this code" / "Dismiss this session" button on the OtpPage.
// Requires the binding cookie to match — only the originating browser
// can cancel its own pending login attempt.
func (h *AuthHandler) CancelOTP(c *fiber.Ctx) error {
	var body struct {
		Email string `json:"email" validate:"required,email"`
	}
	if err := c.BodyParser(&body); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(body); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	bindingToken := c.Cookies(services.OTPBindingCookieName)
	_ = h.service.CancelOTP(c.UserContext(), body.Email, bindingToken)
	// Always clear the cookie on this side too — even if the cancel
	// was a no-op (binding mismatch / already-used / expired), the
	// requesting browser is done with the session.
	c.Cookie(&fiber.Cookie{
		Name:     services.OTPBindingCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		MaxAge:   -1,
		HTTPOnly: true,
		Secure:   c.Secure(),
		SameSite: "Lax",
	})
	if h.auditService != nil {
		h.auditService.LogAction(c.UserContext(), "", body.Email, "", "otp.cancel", "auth", "", "Pending OTP dismissed by user", c.IP(), c.Get("User-Agent"), "success", nil)
	}
	return response.SuccessMessage(c, "Pending login code dismissed", nil)
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
