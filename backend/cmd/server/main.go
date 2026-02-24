package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/handlers"
	"github.com/betazeninfotech/whm-cpanel-management/internal/middleware"
	"github.com/betazeninfotech/whm-cpanel-management/internal/routes"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/logger"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/rs/zerolog/log"
)

func main() {
	// Load config
	cfg := config.Load()
	logger.Setup(cfg.LogLevel)

	log.Info().Str("env", cfg.AppEnv).Msg("Starting ServerPanel")

	// Connect to MongoDB
	db, err := database.Connect(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to MongoDB")
	}
	defer database.Disconnect()

	// Initialize services
	authService := services.NewAuthService(db, cfg)
	domainService := services.NewDomainService(db)
	appService := services.NewAppService(db)
	databaseService := services.NewDatabaseService(db)
	emailService := services.NewEmailService(db)
	dnsService := services.NewDNSService(db)
	sslService := services.NewSSLService(db)
	backupService := services.NewBackupService(db)
	wordpressService := services.NewWordPressService(db)
	firewallService := services.NewFirewallService(db)
	softwareService := services.NewSoftwareService(db)
	monitoringService := services.NewMonitoringService(db)
	logService := services.NewLogService(db)
	cronService := services.NewCronService(db)
	fileService := services.NewFileService(db)
	sshKeyService := services.NewSSHKeyService(db)
	processService := services.NewProcessService(db)
	resourceService := services.NewResourceService(db)
	notificationService := services.NewNotificationService(db)
	auditService := services.NewAuditService(db)
	configService := services.NewConfigService(db)
	maintenanceService := services.NewMaintenanceService(db)
	deployService := services.NewDeployService(db)

	// Initialize handlers
	authHandler := handlers.NewAuthHandler(authService)
	domainHandler := handlers.NewDomainHandler(domainService)
	appHandler := handlers.NewAppHandler(appService)
	databaseHandler := handlers.NewDatabaseHandler(databaseService)
	emailHandler := handlers.NewEmailHandler(emailService)
	dnsHandler := handlers.NewDNSHandler(dnsService)
	sslHandler := handlers.NewSSLHandler(sslService)
	backupHandler := handlers.NewBackupHandler(backupService)
	wordpressHandler := handlers.NewWordPressHandler(wordpressService)
	firewallHandler := handlers.NewFirewallHandler(firewallService)
	softwareHandler := handlers.NewSoftwareHandler(softwareService)
	monitoringHandler := handlers.NewMonitoringHandler(monitoringService)
	logHandler := handlers.NewLogHandler(logService)
	cronHandler := handlers.NewCronHandler(cronService)
	fileHandler := handlers.NewFileHandler(fileService)
	sshKeyHandler := handlers.NewSSHKeyHandler(sshKeyService)
	processHandler := handlers.NewProcessHandler(processService)
	resourceHandler := handlers.NewResourceHandler(resourceService)
	notificationHandler := handlers.NewNotificationHandler(notificationService)
	auditHandler := handlers.NewAuditHandler(auditService)
	configHandler := handlers.NewConfigHandler(configService)
	maintenanceHandler := handlers.NewMaintenanceHandler(maintenanceService)
	deployHandler := handlers.NewDeployHandler(deployService)

	// Create Fiber app
	app := fiber.New(fiber.Config{
		AppName:      "ServerPanel",
		BodyLimit:    500 * 1024 * 1024, // 500 MB
		ErrorHandler: customErrorHandler,
	})

	// Global middleware
	app.Use(recover.New())
	app.Use(middleware.CORS())
	app.Use(middleware.RequestLogger())

	// Health check
	app.Get("/api/v1/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok", "service": "serverpanel"})
	})

	// Register auth routes (shared between WHM and cPanel)
	routes.RegisterAuthRoutes(app, cfg, authHandler)

	// Register WHM routes (vendor panel)
	whmHandlers := &routes.WHMHandlers{
		Domain:       domainHandler,
		App:          appHandler,
		Database:     databaseHandler,
		Email:        emailHandler,
		DNS:          dnsHandler,
		SSL:          sslHandler,
		Backup:       backupHandler,
		WordPress:    wordpressHandler,
		Firewall:     firewallHandler,
		Software:     softwareHandler,
		Monitoring:   monitoringHandler,
		Log:          logHandler,
		Cron:         cronHandler,
		File:         fileHandler,
		SSHKey:       sshKeyHandler,
		Process:      processHandler,
		Resource:     resourceHandler,
		Notification: notificationHandler,
		Audit:        auditHandler,
		Config:       configHandler,
		Maintenance:  maintenanceHandler,
		Deploy:       deployHandler,
		User:         authHandler,
	}
	routes.RegisterWHMRoutes(app, cfg, whmHandlers)

	// Register cPanel routes (customer panel)
	routes.RegisterCPanelRoutes(app, cfg, whmHandlers)

	// Serve WHM React SPA
	app.Static("/whm", "./frontend/apps/whm/dist")
	app.Get("/whm/*", func(c *fiber.Ctx) error {
		return c.SendFile("./frontend/apps/whm/dist/index.html")
	})

	// Serve cPanel React SPA
	app.Static("/cpanel", "./frontend/apps/cpanel/dist")
	app.Get("/cpanel/*", func(c *fiber.Ctx) error {
		return c.SendFile("./frontend/apps/cpanel/dist/index.html")
	})

	// Root redirect based on role
	app.Get("/", middleware.OptionalAuth(cfg), func(c *fiber.Ctx) error {
		if role, ok := c.Locals("role").(string); ok && role == "customer" {
			return c.Redirect("/cpanel/")
		}
		return c.Redirect("/whm/")
	})

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		log.Info().Msg("Shutting down server...")
		_ = app.Shutdown()
	}()

	// Start server
	addr := ":" + cfg.ServerPort
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		log.Info().Str("addr", addr).Msg("Starting HTTPS server")
		if err := app.ListenTLS(addr, cfg.TLSCert, cfg.TLSKey); err != nil {
			log.Fatal().Err(err).Msg("Server failed")
		}
	} else {
		log.Info().Str("addr", addr).Msg("Starting HTTP server")
		if err := app.Listen(addr); err != nil {
			log.Fatal().Err(err).Msg("Server failed")
		}
	}
}

func customErrorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
	}
	return c.Status(code).JSON(fiber.Map{
		"success": false,
		"error": fiber.Map{
			"code":    "INTERNAL_ERROR",
			"message": err.Error(),
		},
	})
}
