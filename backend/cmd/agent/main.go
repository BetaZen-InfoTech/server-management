package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/routes"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/logger"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/rs/zerolog/log"
)

func main() {
	cfg := config.Load()
	logger.Setup(cfg.LogLevel)

	log.Info().Msg("Starting Betazen Server Panel Agent")

	app := fiber.New(fiber.Config{
		AppName: "Betazen Server Panel Agent",
	})

	app.Use(recover.New())

	// API key authentication for agent
	app.Use(func(c *fiber.Ctx) error {
		if c.Path() == "/api/v1/health" {
			return c.Next()
		}
		apiKey := c.Get("X-Agent-Key")
		if apiKey != cfg.AgentAPIKey {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"success": false,
				"error":   "Invalid agent API key",
			})
		}
		return c.Next()
	})

	// Standalone agent daemon doesn't dispatch webhooks — that's the panel
	// server's job. Pass nil so the mail-incoming route stays unmounted
	// here. The Sieve helper POSTs to the panel server, not this binary.
	routes.RegisterAgentRoutes(app, nil)

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		log.Info().Msg("Shutting down agent...")
		_ = app.Shutdown()
	}()

	addr := ":" + cfg.AgentPort
	if cfg.AgentTLSCert != "" && cfg.AgentTLSKey != "" {
		log.Info().Str("addr", addr).Msg("Starting Agent HTTPS server")
		if err := app.ListenTLS(addr, cfg.AgentTLSCert, cfg.AgentTLSKey); err != nil {
			log.Fatal().Err(err).Msg("Agent failed")
		}
	} else {
		log.Info().Str("addr", addr).Msg("Starting Agent HTTP server")
		if err := app.Listen(addr); err != nil {
			log.Fatal().Err(err).Msg("Agent failed")
		}
	}
}
