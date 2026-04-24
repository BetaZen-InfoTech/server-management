package routes

import (
	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/handlers"
	"github.com/betazeninfotech/whm-cpanel-management/internal/middleware"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/mongo"
)

func RegisterAuthRoutes(app *fiber.App, cfg *config.Config, db *mongo.Database, h *handlers.AuthHandler) {
	auth := app.Group("/api/v1/auth")

	auth.Post("/login", middleware.LoginRateLimiter(), h.Login)
	auth.Post("/refresh", h.Refresh)
	auth.Post("/logout", h.Logout)
	auth.Post("/forgot-password", h.ForgotPassword)
	auth.Post("/reset-password", h.ResetPassword)

	// Passwordless email-OTP login. The endpoints cooperate to defeat
	// the "magic URL forwarded/pasted into a different browser = instant
	// takeover" hole without losing the one-click UX:
	//   request  — emails a 10-char code + magic URL, stamps the
	//              requesting browser with an HTTP-only bz_otp_bind
	//              cookie, saves sha256(cookie) on the OTP doc.
	//   verify   — same browser: consume + JWT pair. Different
	//              browser with correct code: stamps handoff_approved
	//              and returns {status:"approved_in_other_browser"}.
	//              Wrong code: 401 + attempts bump.
	//   poll     — cookie-only; the originating browser polls this to
	//              learn when a magic-link handoff arrived.
	//   complete — cookie-only; consumes an approved OTP and issues
	//              the JWT pair. Only the browser with the matching
	//              cookie can call it — an attacker who triggered
	//              the approval by clicking a forwarded URL gets
	//              nothing (no cookie).
	//   cancel   — cookie-gated; revokes pending OTPs so the emailed
	//              link/code can't be redeemed after a perceived leak.
	//
	// LoginRateLimiter() guards every endpoint so an abuser can't pump
	// any of them.
	auth.Post("/otp/request", middleware.LoginRateLimiter(), h.RequestOTP)
	auth.Post("/otp/verify", middleware.LoginRateLimiter(), h.VerifyOTP)
	auth.Post("/otp/poll", middleware.LoginRateLimiter(), h.PollOTP)
	auth.Post("/otp/complete", middleware.LoginRateLimiter(), h.CompleteOTP)
	auth.Post("/otp/cancel", middleware.LoginRateLimiter(), h.CancelOTP)

	// Self-service profile — the signed-in user manages their own name,
	// email, and password from the Profile page. Gated behind the normal
	// auth middleware so anonymous callers can't probe.
	me := auth.Group("/me", middleware.Auth(cfg, db))
	me.Get("/", h.Me)
	me.Patch("/", h.UpdateMe)
	me.Post("/password", h.ChangeMyPassword)
	// Recent login history — users see their own device/IP/location
	// trail from the Account → Sessions page.
	me.Get("/sessions", h.ListMySessions)

	// 2FA routes (require authentication)
	twoFA := auth.Group("/2fa", middleware.Auth(cfg, db))
	twoFA.Post("/enable", h.Enable2FA)
	twoFA.Post("/verify", h.Verify2FA)
	twoFA.Post("/disable", h.Disable2FA)
}
