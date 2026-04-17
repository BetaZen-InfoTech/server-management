package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/handlers"
	"github.com/betazeninfotech/whm-cpanel-management/internal/middleware"
	"github.com/betazeninfotech/whm-cpanel-management/internal/routes"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/crypto"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/logger"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/version"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/websocket/v2"
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

	// Multi-tenant backfill: idempotent, no-op on fresh installs and on
	// every boot after the first. Sets tenant_id on legacy user records so
	// the new vendor scoping has data to filter on.
	if err := services.BackfillTenantIDs(context.Background(), db); err != nil {
		log.Warn().Err(err).Msg("tenant_id backfill failed")
	}

	// Initialize services
	authService := services.NewAuthService(db, cfg)
	dnsService := services.NewDNSService(db)
	emailService := services.NewEmailService(db, cfg.JWTSecret)
	sslService := services.NewSSLService(db)
	domainService := services.NewDomainService(db, dnsService, sslService, emailService, services.DomainServiceConfig{
		SSLEmail:  "admin@betazeninfotech.com",
		JWTSecret: cfg.JWTSecret,
		ServerIP:  cfg.ServerIP,
	})
	appService := services.NewAppService(db)
	databaseService := services.NewDatabaseService(db)
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
	maintenanceService := services.NewMaintenanceService(db, cfg.Domain, cfg.ServerIP)
	deployService := services.NewDeployService(db)

	// Load the AES-GCM key used to encrypt stored GitHub PATs. In production
	// we fail fast if the operator hasn't set one; in dev we auto-generate an
	// ephemeral key and warn, so the panel still boots without hand-holding.
	encKey, err := crypto.LoadKey(cfg.AppEncryptionKey)
	if err != nil {
		if cfg.IsProduction() {
			log.Fatal().Err(err).Msg("APP_ENCRYPTION_KEY is required in production")
		}
		ephemeral, _ := crypto.GenerateKey()
		encKey, _ = crypto.LoadKey(ephemeral)
		log.Warn().Msg("APP_ENCRYPTION_KEY not set — generated ephemeral dev key; stored PATs will be unrecoverable after restart")
	}
	webhookBase := cfg.PublicWebhookBaseURL
	if webhookBase == "" {
		webhookBase = "https://" + cfg.Domain
	}
	projectService := services.NewProjectService(db, encKey, webhookBase, "admin@"+cfg.Domain, cfg.ServerIP)

	dashboardService := services.NewDashboardService(db)
	userService := services.NewUserService(db)
	userService.SetDomainService(domainService)
	packageService := services.NewPackageService(db)
	transferService := services.NewTransferService(db, cfg.ServerIP, cfg.Domain)
	// Resume any transfers that were in progress when the backend went down.
	// Steps are idempotent, so restarting from step 1 is safe.
	if err := transferService.ResumeRunningTransfers(context.Background()); err != nil {
		log.Warn().Err(err).Msg("Failed to resume running transfers on startup")
	}

	// Initialize handlers
	authHandler := handlers.NewAuthHandler(authService)
	authHandler.SetAuditService(auditService)
	domainHandler := handlers.NewDomainHandler(domainService)
	appHandler := handlers.NewAppHandler(appService)
	databaseHandler := handlers.NewDatabaseHandler(databaseService)
	emailHandler := handlers.NewEmailHandler(emailService)
	dnsHandler := handlers.NewDNSHandler(dnsService)
	sslHandler := handlers.NewSSLHandler(sslService)
	wordpressHandler := handlers.NewWordPressHandler(wordpressService)
	backupHandler := handlers.NewBackupHandler(backupService, wordpressService)
	transferService.SetWordPressService(wordpressService)
	firewallHandler := handlers.NewFirewallHandler(firewallService)
	softwareHandler := handlers.NewSoftwareHandler(softwareService)
	monitoringHandler := handlers.NewMonitoringHandler(monitoringService)
	logHandler := handlers.NewLogHandler(logService)
	cronHandler := handlers.NewCronHandler(cronService)
	fileHandler := handlers.NewFileHandler(fileService)
	sshKeyHandler := handlers.NewSSHKeyHandler(sshKeyService, db)
	processHandler := handlers.NewProcessHandler(processService)
	resourceHandler := handlers.NewResourceHandler(resourceService)
	notificationHandler := handlers.NewNotificationHandler(notificationService)
	auditHandler := handlers.NewAuditHandler(auditService)
	configHandler := handlers.NewConfigHandler(configService)
	maintenanceHandler := handlers.NewMaintenanceHandler(maintenanceService)
	deployHandler := handlers.NewDeployHandler(deployService)
	projectHandler := handlers.NewProjectHandler(projectService)
	dashboardHandler := handlers.NewDashboardHandler(dashboardService)
	userHandler := handlers.NewUserHandler(userService)
	packageHandler := handlers.NewPackageHandler(packageService)
	transferHandler := handlers.NewTransferHandler(transferService)

	// Start background metrics collector (every 60 seconds)
	metricsCtx, metricsCancel := context.WithCancel(context.Background())
	defer metricsCancel()
	monitoringService.StartMetricsCollector(metricsCtx, 60*time.Second)

	// Create Fiber app
	app := fiber.New(fiber.Config{
		AppName:      "ServerPanel",
		BodyLimit:    500 * 1024 * 1024, // 500 MB
		ReadTimeout:  30 * time.Minute,  // Long timeout for install operations
		WriteTimeout: 30 * time.Minute,
		IdleTimeout:  5 * time.Minute,
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

	// Version — public, unauthenticated so the login page + the topbar can
	// render the product name/number before a user is signed in. Source of
	// truth is pkg/version so every surface reads the same constants.
	app.Get("/api/v1/version", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"success": true, "data": version.Get()})
	})

	// WebSocket: real-time install terminal output
	app.Use("/ws", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})
	app.Get("/ws/install-terminal", websocket.New(handlers.HandleInstallTerminalWS))
	app.Get("/ws/terminal", websocket.New(handlers.NewTerminalWSHandler(cfg.JWTSecret, db)))

	// Register auth routes (shared between WHM and cPanel)
	routes.RegisterAuthRoutes(app, cfg, authHandler)

	// Register WHM routes (vendor panel)
	whmHandlers := &routes.WHMHandlers{
		AuditService: auditService,
		Domain:       domainHandler,
		App:          appHandler,
		Package:      packageHandler,
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
		Project:      projectHandler,
		User:         authHandler,
		UserMgmt:     userHandler,
		Dashboard:    dashboardHandler,
		Transfer:     transferHandler,
	}
	routes.RegisterWHMRoutes(app, cfg, whmHandlers)

	// Register cPanel routes (customer panel)
	routes.RegisterCPanelRoutes(app, cfg, whmHandlers)

	// Serve WHM React SPA.
	//
	// Hashed asset bundles (index-<hash>.js / .css) live under /assets/ and
	// are safe to cache aggressively — their filenames change on every build
	// so a client never needs to re-fetch the same URL. Everything else
	// (notably index.html) must be no-store so browsers can't keep serving
	// a stale HTML that references an old bundle name after a deploy.
	//
	// NOTE: Do NOT register a redirect handler for the bare /whm path. Fiber
	// v2 runs in non-strict-routing mode by default, so a handler for "/whm"
	// also matches "/whm/" — a redirect to "/whm/" would match its own
	// handler and loop forever (the exact bug that made 'Edit not work':
	// the whole WHM page never loaded past the redirect).
	app.Static("/whm/assets", "./frontend/apps/whm/dist/assets", fiber.Static{
		MaxAge: 31536000, // 1 year — safe because filenames are hashed
	})
	sendWHMIndex := func(c *fiber.Ctx) error {
		c.Set("Cache-Control", "no-store, no-cache, must-revalidate")
		c.Set("Pragma", "no-cache")
		c.Set("Expires", "0")
		return c.SendFile("./frontend/apps/whm/dist/index.html")
	}
	app.Get("/whm", sendWHMIndex)
	app.Get("/whm/", sendWHMIndex)
	app.Get("/whm/*", sendWHMIndex)

	// Serve cPanel React SPA (same split).
	app.Static("/cpanel/assets", "./frontend/apps/cpanel/dist/assets", fiber.Static{
		MaxAge: 31536000,
	})
	sendCPanelIndex := func(c *fiber.Ctx) error {
		c.Set("Cache-Control", "no-store, no-cache, must-revalidate")
		c.Set("Pragma", "no-cache")
		c.Set("Expires", "0")
		return c.SendFile("./frontend/apps/cpanel/dist/index.html")
	}
	app.Get("/cpanel", sendCPanelIndex)
	app.Get("/cpanel/", sendCPanelIndex)
	app.Get("/cpanel/*", sendCPanelIndex)

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
