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

	// 2FA routes (require authentication)
	twoFA := auth.Group("/2fa", middleware.Auth(cfg, db))
	twoFA.Post("/enable", h.Enable2FA)
	twoFA.Post("/verify", h.Verify2FA)
	twoFA.Post("/disable", h.Disable2FA)
}
