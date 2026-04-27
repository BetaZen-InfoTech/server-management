package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
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

	log.Info().Str("env", cfg.AppEnv).Msg("Starting Betazen Server Panel")

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
	databaseService.SetPMASignonSecret(cfg.PMASignonSecret)
	// Hostname surfaced in the database Connection modal. Internal
	// panel ops still use localhost; this just rewrites the user-
	// copyable URL so it works from outside the box.
	databaseService.SetPublicHosts(cfg.MongoPublicHost, cfg.MySQLPublicHost, cfg.ServerIP)
	backupService := services.NewBackupService(db)
	wordpressService := services.NewWordPressService(db)
	firewallService := services.NewFirewallService(db)
	softwareService := services.NewSoftwareService(db)
	// Wire the runtime-default lookup so app/project deploys with an
	// empty runtime_version pick up the host operator's pinned default
	// (Software → "Set as default" radio). The provider is read on
	// every deploy so a default change takes effect on the next
	// install/build without restarting the backend.
	services.SetRuntimeDefaultProvider(func(appType string) string {
		defaults := softwareService.GetRuntimeDefaults(context.Background())
		switch strings.ToLower(appType) {
		case "node", "nodejs":
			return defaults["nodejs"]
		case "go", "golang":
			return defaults["go"]
		case "ruby":
			return defaults["ruby"]
		case "python":
			return defaults["python"]
		case "php":
			return defaults["php"]
		}
		return ""
	})
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

	// Load the AES-GCM key used to encrypt stored GitHub PATs. If the
	// operator hasn't set one we generate an ephemeral key and warn loudly
	// — losing the whole panel because one optional feature isn't
	// configured would be worse than the Deploy Software feature losing
	// its saved PATs after a restart. Operators who use that feature set
	// APP_ENCRYPTION_KEY persistently; everyone else gets a noisy boot
	// log and the rest of the panel works.
	encKey, err := crypto.LoadKey(cfg.AppEncryptionKey)
	if err != nil {
		ephemeral, _ := crypto.GenerateKey()
		encKey, _ = crypto.LoadKey(ephemeral)
		if cfg.IsProduction() {
			log.Warn().Msg("APP_ENCRYPTION_KEY not set — using ephemeral key. Any PATs stored via Deploy Software will be unrecoverable on restart. Set APP_ENCRYPTION_KEY in /opt/serverpanel/.env to persist them.")
		} else {
			log.Warn().Msg("APP_ENCRYPTION_KEY not set — generated ephemeral dev key")
		}
	}
	webhookBase := cfg.PublicWebhookBaseURL
	if webhookBase == "" {
		webhookBase = "https://" + cfg.Domain
	}
	projectService := services.NewProjectService(db, encKey, webhookBase, "admin@"+cfg.Domain, cfg.ServerIP)

	// Panel-owned outgoing mail: one SMTP relay used for password resets,
	// domain-expiry warnings, notifications. Separate from tenant
	// mailboxes (managed by EmailService on Postfix/Dovecot). Warm-
	// starts from the saved config in Mongo so a process restart
	// doesn't break the forgot-password flow.
	panelMailService := services.NewPanelMailService(db, encKey)
	// Fresh-install bootstrap: when no operator-saved SMTP config
	// exists yet, auto-configure the panel mailer to use the local
	// Postfix relay (127.0.0.1:25) with admin@<panel-domain> as the
	// sender. Idempotent — operator-owned config always wins. This
	// closes the gap where a fresh install would silently drop
	// password-reset emails into journalctl until someone manually
	// filled in Server Settings → Outgoing Mail.
	if err := panelMailService.AutoBootstrap(context.Background(), cfg.Domain); err != nil {
		log.Warn().Err(err).Msg("panel-mail auto-bootstrap failed (non-fatal)")
	}
	// Hand the shared mailer to AuthService so ForgotPassword can send
	// the reset link without a separate wiring step.
	authService.SetMailer(panelMailService.Mailer())

	// NotifierService is the single fan-out for panel events that need
	// an email: SMTP configured, new domain added, SSL active / failed,
	// registrar expiry warnings. Every service that wants to notify
	// gets a pointer to this instance below; none of them ever touches
	// mailer.Mailer directly.
	notifierService := services.NewNotifierService(db, panelMailService.Mailer(), cfg.Domain)
	panelMailService.SetNotifier(notifierService)
	domainService.SetNotifier(notifierService)
	sslService.SetNotifier(notifierService)

	dashboardService := services.NewDashboardService(db)
	userService := services.NewUserService(db)
	userService.SetDomainService(domainService)
	packageService := services.NewPackageService(db)
	transferService := services.NewTransferService(db, cfg.ServerIP, cfg.Domain)
	// Wire the ConfigService in so the transfer pipeline can run its
	// post-import IP sweep (DNS + SPF + env + panel vhost) at the end.
	transferService.SetConfigService(configService)
	// Wire the EmailService in so the transfer pipeline can run its
	// post-import mail-stack repair (chroot resolver sync + DKIM
	// rewire per imported domain) at the end.
	transferService.SetEmailService(emailService)
	// Wire the MaintenanceService so the transfer pipeline can mirror
	// the source's server-wide maintenance flag onto the destination
	// (user requirement: if source is in maintenance, destination
	// should come up in maintenance too — preserves the operator's
	// intent across the DNS cutover).
	transferService.SetMaintenanceService(maintenanceService)
	// Resume any transfers that were in progress when the backend went down.
	// Steps are idempotent, so restarting from step 1 is safe.
	if err := transferService.ResumeRunningTransfers(context.Background()); err != nil {
		log.Warn().Err(err).Msg("Failed to resume running transfers on startup")
	}
	// Transfer-token service: minted by THIS panel when it's acting as the
	// SOURCE side of a migration, redeemed by the destination panel via the
	// public /api/v1/transfer/redeem endpoint. The janitor sweeps expired
	// tokens (and their authorized_keys lines) every hour.
	transferTokenService := services.NewTransferTokenService(db)
	transferTokenService.StartJanitor(context.Background())
	transferService.SetTokenService(transferTokenService)

	// Reboot-recovery sweep: every panel-managed sp-app-* / sp-proj-*
	// unit gets enable + reset-failed + start, so a VPS reboot brings
	// everything back without the operator clicking Start on every row.
	// Runs in the background so it can't delay the HTTP listener.
	go services.RunBootReconcile(context.Background(), db)

	// Initialize handlers
	authHandler := handlers.NewAuthHandler(authService)
	authHandler.SetAuditService(auditService)
	domainHandler := handlers.NewDomainHandler(domainService, db)
	appHandler := handlers.NewAppHandler(appService)
	webhookHandler := handlers.NewWebhookHandler(appService)
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
	// Hand the user service to the resource handler so the per-vendor
	// rollup can reuse the shared `du -sb` cache instead of running a
	// parallel disk walk on every page render.
	resourceHandler.SetUserService(userService)
	notificationHandler := handlers.NewNotificationHandler(notificationService)
	auditHandler := handlers.NewAuditHandler(auditService)
	configHandler := handlers.NewConfigHandler(configService)
	panelMailHandler := handlers.NewPanelMailHandler(panelMailService)
	maintenanceHandler := handlers.NewMaintenanceHandler(maintenanceService)
	deployHandler := handlers.NewDeployHandler(deployService)
	projectHandler := handlers.NewProjectHandler(projectService)
	dashboardHandler := handlers.NewDashboardHandler(dashboardService)
	userHandler := handlers.NewUserHandler(userService)
	// Wire AuthService into UserHandler so AdminImpersonateVendor can mint
	// short-lived tokens for the "Login as vendor" button on WHM Vendors.
	userHandler.SetAuthService(authService)
	packageHandler := handlers.NewPackageHandler(packageService)
	// Wire the user service so the Approve handler can swap a vendor's
	// package on approval without PackageService needing a direct
	// UserService dep (which would create an import cycle).
	packageHandler.SetUserService(userService)
	transferHandler := handlers.NewTransferHandler(transferService)
	transferTokenHandler := handlers.NewTransferTokenHandler(transferTokenService, db)

	// Start background metrics collector (every 60 seconds)
	metricsCtx, metricsCancel := context.WithCancel(context.Background())
	defer metricsCancel()
	monitoringService.StartMetricsCollector(metricsCtx, 60*time.Second)

	// Install the terminal-jail rcfile. Tenant shells (customer /
	// vendor_staff / developer / support) get bash spawned with
	// --rcfile /etc/serverpanel/jail.bashrc so the interactive prompt
	// refuses cd outside $HOME. Best-effort — logged to stderr if it
	// fails (e.g. read-only /etc), and the terminal handler falls back
	// to a plain login shell when the rcfile is absent.
	if err := agent.EnsureTerminalJailRcfile(context.Background()); err != nil {
		log.Warn().Err(err).Msg("failed to install terminal jail rcfile; tenant shells will run without UX sandbox")
	}

	// Create Fiber app
	app := fiber.New(fiber.Config{
		AppName:      "Betazen Server Panel",
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

	// Public transfer-token redeem endpoint. Hit by a destination panel
	// during a migration: no JWT (the token IS the auth), but the handler
	// applies a per-IP rate limit so the bcrypt comparison can't be
	// weaponised for CPU exhaustion. Lives at the public root because
	// /api/v1/whm/* is gated on JWT auth.
	app.Post("/api/v1/transfer/redeem", transferTokenHandler.Redeem)

	// Public UI feature flags — the login pages need to know whether to
	// render the demo-credentials block before any user is signed in, so
	// this is exposed unauthenticated. Only non-sensitive booleans land
	// here; everything secret stays behind the authenticated /config routes.
	app.Get("/api/v1/public-settings", configHandler.PublicSettings)

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
	routes.RegisterAuthRoutes(app, cfg, db, authHandler)

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
		PanelMail:    panelMailHandler,
		Maintenance:  maintenanceHandler,
		Deploy:       deployHandler,
		Project:      projectHandler,
		User:         authHandler,
		UserMgmt:     userHandler,
		Dashboard:    dashboardHandler,
		Transfer:     transferHandler,
		TransferTok:  transferTokenHandler,
		Webhook:      webhookHandler,
	}
	routes.RegisterWHMRoutes(app, cfg, db, whmHandlers)

	// Register cPanel routes (customer panel)
	routes.RegisterCPanelRoutes(app, cfg, db, whmHandlers)

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

	// Serve the vendor / customer panel SPA. Canonical mount point is
	// /user-panel/* — this is the workspace for every non-platform-owner
	// role (vendor_admin, vendor_staff, developer, support, customer).
	// The historical /cpanel/* prefix still 301-redirects below so any
	// bookmark from the prior URL keeps working.
	app.Static("/user-panel/assets", "./frontend/apps/cpanel/dist/assets", fiber.Static{
		MaxAge: 31536000,
	})
	sendUserPanelIndex := func(c *fiber.Ctx) error {
		c.Set("Cache-Control", "no-store, no-cache, must-revalidate")
		c.Set("Pragma", "no-cache")
		c.Set("Expires", "0")
		return c.SendFile("./frontend/apps/cpanel/dist/index.html")
	}
	app.Get("/user-panel", sendUserPanelIndex)
	app.Get("/user-panel/", sendUserPanelIndex)
	app.Get("/user-panel/*", sendUserPanelIndex)

	// Legacy /cpanel/* → /user-panel/* 301 alias. One-release deprecation
	// so existing bookmarks don't break the day we ship the rename. Asset
	// requests get the same treatment so a stale index.html that still
	// points at /cpanel/assets/index.js doesn't 404.
	cpanelAlias := func(c *fiber.Ctx) error {
		newPath := "/user-panel" + strings.TrimPrefix(c.Path(), "/cpanel")
		if newPath == "/user-panel" {
			newPath = "/user-panel/"
		}
		if q := c.Request().URI().QueryString(); len(q) > 0 {
			newPath += "?" + string(q)
		}
		return c.Redirect(newPath, fiber.StatusMovedPermanently)
	}
	app.Get("/cpanel", cpanelAlias)
	app.Get("/cpanel/", cpanelAlias)
	app.Get("/cpanel/*", cpanelAlias)

	// Root redirect based on role. vendor_owner is the only role that
	// belongs in WHM; every other authenticated role goes to the user
	// panel. Unauthenticated visitors land on WHM's login page (the
	// user-panel login page lives at /user-panel/login and is reached
	// via the explicit /user-panel/ URL).
	app.Get("/", middleware.OptionalAuth(cfg, db), func(c *fiber.Ctx) error {
		role, _ := c.Locals("role").(string)
		switch role {
		case "vendor_owner":
			return c.Redirect("/whm/")
		case "vendor_admin", "vendor_staff", "developer", "support", "customer":
			return c.Redirect("/user-panel/")
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

	// Trash purger — every 6 hours, permanently delete vendors whose
	// 15-day trash window has elapsed. Runs one pass immediately at
	// startup so a crash+restart doesn't let a due-today trash linger
	// an extra 6 hours. Errors are logged but don't stop the loop —
	// the next tick will retry.
	go func() {
		purge := func() {
			bg, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			n, err := userService.PurgeExpiredTrash(bg)
			if err != nil {
				log.Warn().Err(err).Msg("trash purge failed")
				return
			}
			if n > 0 {
				log.Info().Int("purged", n).Msg("trash purger removed expired vendors")
			}
		}
		purge()
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			purge()
		}
	}()

	// Daily domain-expiry sweep — walks every domain with ExpiresOn set
	// and sends the most-urgent unsent warning in the ladder
	// (30 / 21 / 14 / 7 / 5 / 3 / 2 / 1 days). The notifier tracks which
	// bucket was last sent on the domain doc so we don't spam, and
	// renewals (ExpiresOn pushed out) auto-reset the tracker. Runs one
	// pass at startup so a boot that lands exactly on a bucket boundary
	// doesn't wait up to 24h to warn. Errors are logged; the loop
	// never exits.
	go func() {
		sweep := func() {
			bg, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			sent, err := notifierService.RunDomainExpirySweep(bg)
			if err != nil {
				log.Warn().Err(err).Msg("domain expiry sweep failed")
				return
			}
			if sent > 0 {
				log.Info().Int("sent", sent).Msg("domain expiry sweep sent warning emails")
			}
		}
		sweep()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			sweep()
		}
	}()

	// Self-healing: if an admin pointed the panel at a custom domain
	// and an LE cert was issued, but the nginx vhost was never upgraded
	// to the SSL variant (happens when they clicked Update Domain on a
	// build that pre-dated the SSL-rewrite fix), restore the :443 block
	// now. Idempotent — bails out if the vhost already has listen 443.
	// Runs in a goroutine so a slow shell-out on the first request
	// doesn't delay server.Listen.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		configService.ReconcilePanelDomain(ctx)
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
