package routes

import (
	"github.com/gofiber/fiber/v2"
)

func RegisterAgentRoutes(app *fiber.App) {
	agent := app.Group("/api/v1")

	agent.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// Agent-specific routes will be added here
	// These are called by the panel server to execute operations on the VPS
}
