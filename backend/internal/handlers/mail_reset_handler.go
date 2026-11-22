package handlers

import (
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/gofiber/fiber/v2"
)

// MailResetHandler powers the PUBLIC, rate-limited webmail self-service flows:
// set/get a mailbox recovery email (re-auth with the mailbox password) and the
// forgot/reset password cycle (emailed link to the recovery address). It is
// consumed by the Betazen Mail webmail (via the mail-suite proxy) — no panel
// JWT is involved, which is why every mutation re-authenticates or requires a
// one-time token.
type MailResetHandler struct {
	email        *services.EmailService
	resetBaseURL string
}

func NewMailResetHandler(email *services.EmailService, webmailPublicURL string) *MailResetHandler {
	base := strings.TrimRight(strings.TrimSpace(webmailPublicURL), "/") + "/mail/reset"
	return &MailResetHandler{email: email, resetBaseURL: base}
}

// SetRecoveryEmail sets (or clears) a mailbox's recovery email. Re-authenticates
// with the mailbox password.
func (h *MailResetHandler) SetRecoveryEmail(c *fiber.Ctx) error {
	var req models.SetMailboxRecoveryEmailRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	if err := h.email.SetMailboxRecoveryEmail(c.UserContext(), req.Email, req.Password, req.RecoveryEmail); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Recovery email saved", nil)
}

// GetRecoveryEmail returns the masked recovery email, re-authenticating with the
// mailbox password.
func (h *MailResetHandler) GetRecoveryEmail(c *fiber.Ctx) error {
	var req models.GetMailboxRecoveryEmailRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	masked, err := h.email.GetMailboxRecoveryEmailMasked(c.UserContext(), req.Email, req.Password)
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, fiber.Map{"recovery_email": masked})
}

// Forgot starts the reset flow. ALWAYS 200 (no account enumeration) — a link is
// emailed only when the mailbox exists and has a recovery address on file.
func (h *MailResetHandler) Forgot(c *fiber.Ctx) error {
	var req models.ForgotMailboxPasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	// Errors here are infra-level only; the method itself never signals whether
	// the address exists. Swallow to keep the response uniform.
	_ = h.email.StartMailboxPasswordReset(c.UserContext(), req.Email, h.resetBaseURL)
	return response.SuccessMessage(c, "If that mailbox has a recovery email on file, a reset link has been sent to it.", nil)
}

// Reset completes the flow with the emailed token + a new password.
func (h *MailResetHandler) Reset(c *fiber.Ctx) error {
	var req models.ResetMailboxPasswordByTokenRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	if errs := validator.Validate(req); errs != nil {
		return response.BadRequest(c, "Validation failed", errs)
	}
	if err := h.email.CompleteMailboxPasswordReset(c.UserContext(), req.Token, req.Password); err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.SuccessMessage(c, "Your password has been reset. You can now sign in with your new password.", nil)
}
