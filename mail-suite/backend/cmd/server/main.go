package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/config"
	"github.com/betazeninfotech/mail-suite/internal/database"
	"github.com/betazeninfotech/mail-suite/internal/handlers"
	"github.com/betazeninfotech/mail-suite/internal/routes"
	"github.com/betazeninfotech/mail-suite/internal/services"
	"github.com/betazeninfotech/mail-suite/pkg/jwt"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("config")
	}
	if lvl, err := zerolog.ParseLevel(cfg.LogLevel); err == nil {
		zerolog.SetGlobalLevel(lvl)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := database.Connect(ctx, cfg.MongoURI, cfg.MongoDBName)
	if err != nil {
		log.Fatal().Err(err).Msg("mongo connect")
	}
	defer db.Close(context.Background())

	if err := database.EnsureIndexes(ctx, db); err != nil {
		log.Warn().Err(err).Msg("ensure indexes")
	}

	jm := jwt.New(cfg.JWTSecret, cfg.JWTAccessExpiry)

	// Services
	authSvc := services.NewAuthService(db, jm, cfg.JWTRefreshExpiry, cfg)
	accSvc := services.NewAccountService(db, cfg)
	sigSvc := services.NewSignatureService(db)
	mailSvc := services.NewMailService(db, accSvc, sigSvc, cfg)
	panel := services.NewBetazenPanelClient(cfg)
	fwdSvc := services.NewForwarderService(db, panel)
	dnsSvc := services.NewDNSService(cfg, panel)
	devSvc := services.NewDeviceService(db)
	draftSvc := services.NewDraftService(db)
	trackSvc := services.NewTrackingService(db, cfg.JWTSecret)
	groupSvc := services.NewContactGroupService(db)
	contactSvc := services.NewContactService(db, groupSvc)
	campaignSvc := services.NewCampaignService(db)
	campaignTemplateSvc := services.NewCampaignTemplateService(db)
	campaignWorker := services.NewCampaignWorker(db, accSvc, cfg)
	campaignWorker.Start(ctx)
	webpushSvc := services.NewWebPushService(db, cfg)
	fcmSvc := services.NewFCMService(db, cfg)
	notifyWorker := services.NewNotifyWorker(db, webpushSvc, fcmSvc)
	notifyWorker.Start(ctx)

	// Handlers
	trackHandler := handlers.NewTrackingHandler(trackSvc)
	unsubHandler := handlers.NewUnsubscribeHandler(contactSvc)
	deps := routes.Deps{
		JWT:          jm,
		Auth:         handlers.NewAuthHandler(authSvc),
		Account:      handlers.NewAccountHandler(accSvc),
		Mail:         handlers.NewMailHandler(mailSvc),
		Signature:    handlers.NewSignatureHandler(sigSvc),
		Forwarder:    handlers.NewForwarderHandler(fwdSvc),
		DNS:          handlers.NewDNSHandler(dnsSvc),
		Push:         handlers.NewPushHandler(devSvc),
		WebPush:      handlers.NewWebPushHandler(webpushSvc),
		Draft:        handlers.NewDraftHandler(draftSvc),
		Tracking:     trackHandler,
		Contact:      handlers.NewContactHandler(contactSvc),
		ContactGroup: handlers.NewContactGroupHandler(groupSvc),
		Campaign:         handlers.NewCampaignHandler(campaignSvc),
		CampaignTemplate: handlers.NewCampaignTemplateHandler(campaignTemplateSvc),
	}

	app := fiber.New(fiber.Config{
		AppName:      "Betazen Mail Suite",
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	})
	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins:     "*",
		AllowMethods:     "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders:     "Authorization,Content-Type",
		ExposeHeaders:    "Content-Disposition",
		AllowCredentials: false,
	}))

	app.Get("/healthz", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"ok": true, "service": "mail-suite", "ts": time.Now().Unix()})
	})

	// PUBLIC email-tracking beacons — deliberately outside /api/v1 so no JWT is
	// required (they're opened by arbitrary mail clients). The open pixel and the
	// click redirect key off the opaque :id / :track_id path param only.
	app.Get("/t/open/:id", trackHandler.Open)
	app.Get("/t/click/:id", trackHandler.Click)

	// PUBLIC one-click unsubscribe (footer link + RFC 8058 List-Unsubscribe-Post).
	// No JWT — opened straight from campaign mail.
	app.Get("/u/:token", unsubHandler.Unsubscribe)
	app.Post("/u/:token", unsubHandler.Unsubscribe)

	routes.Register(app, deps)

	// Webmail SPA: serve the built /mail-suite/webmail/dist at /mail/
	// and bounce / → /mail/ so the root path doesn't return Fiber's
	// stock "Cannot GET /". The Vite build sets base: '/mail/'; the
	// asset paths in index.html therefore already include that prefix.
	if cfg.WebmailDir != "" {
		if _, err := os.Stat(cfg.WebmailDir); err == nil {
			indexPath := filepath.Join(cfg.WebmailDir, "index.html")
			app.Static("/mail", cfg.WebmailDir, fiber.Static{
				Index:    "index.html",
				Compress: true,
				MaxAge:   300,
			})
			// SPA fallback — any /mail/* path the static middleware
			// didn't resolve to a file gets index.html so React Router
			// owns it.
			app.Get("/mail/*", func(c *fiber.Ctx) error {
				return c.SendFile(indexPath)
			})
			app.Get("/", func(c *fiber.Ctx) error {
				return c.Redirect("/mail/", fiber.StatusFound)
			})
			log.Info().Str("dir", cfg.WebmailDir).Msg("serving webmail SPA at /mail/")
		} else {
			log.Warn().Str("dir", cfg.WebmailDir).Err(err).Msg("WEBMAIL_DIR set but unreadable; SPA disabled")
		}
	}

	go func() {
		log.Info().Str("port", cfg.ServerPort).Msg("listening")
		if err := app.Listen(":" + cfg.ServerPort); err != nil {
			log.Fatal().Err(err).Msg("listen")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Info().Msg("shutting down")
	_ = app.Shutdown()
}
