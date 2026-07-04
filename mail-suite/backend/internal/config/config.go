package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv   string
	LogLevel string

	MongoURI    string
	MongoDBName string

	JWTSecret        string
	JWTAccessExpiry  time.Duration
	JWTRefreshExpiry time.Duration

	ServerPort string
	PublicURL  string

	// WebmailDir is the on-disk path holding the built webmail SPA
	// (mail-suite/webmail/dist). When set, the backend serves it under
	// /mail/ and redirects / → /mail/. When blank, only the API is
	// served and GET / returns 404.
	WebmailDir string

	// Local mail server (Postfix/Dovecot on the same host)
	IMAPHost string
	IMAPPort int
	SMTPHost string
	SMTPPort int

	// Betazen panel — used for DNS upserts + provisioning bridges
	BetazenPanelURL   string
	BetazenPanelToken string

	// FCM service account JSON path (mobile app push, future)
	FCMCredentialsFile string
	FCMProjectID       string

	// Web Push (VAPID) — browser/PWA notifications for the webmail. When both
	// keys are set, the new-mail poller pushes notifications to subscribed
	// browsers via the standard Web Push protocol (no Firebase SDK needed).
	VapidPublic  string
	VapidPrivate string
	VapidSubject string

	// WebAuthn
	WebAuthnRPID          string
	WebAuthnRPDisplayName string
	WebAuthnOrigins       []string

	// OAuth2 provider
	OAuthIssuer string
}

func Load() (*Config, error) {
	_ = godotenv.Load(".env.local")
	_ = godotenv.Load(".env")

	access, err := time.ParseDuration(getenv("JWT_ACCESS_EXPIRY", "15m"))
	if err != nil {
		return nil, fmt.Errorf("invalid JWT_ACCESS_EXPIRY: %w", err)
	}
	refresh, err := time.ParseDuration(getenv("JWT_REFRESH_EXPIRY", "168h"))
	if err != nil {
		return nil, fmt.Errorf("invalid JWT_REFRESH_EXPIRY: %w", err)
	}

	imapPort, _ := strconv.Atoi(getenv("IMAP_PORT", "143"))
	smtpPort, _ := strconv.Atoi(getenv("SMTP_PORT", "587"))

	c := &Config{
		AppEnv:                getenv("APP_ENV", "development"),
		LogLevel:              getenv("LOG_LEVEL", "info"),
		MongoURI:              getenv("MONGO_URI", "mongodb://localhost:27017"),
		MongoDBName:           getenv("MONGO_DB", "mail_suite"),
		JWTSecret:             requireEnv("JWT_SECRET", "change-me-mail-suite-secret"),
		JWTAccessExpiry:       access,
		JWTRefreshExpiry:      refresh,
		ServerPort:            getenv("SERVER_PORT", "9090"),
		PublicURL:             getenv("PUBLIC_URL", "http://localhost:9090"),
		WebmailDir:            getenv("WEBMAIL_DIR", ""),
		IMAPHost:              getenv("IMAP_HOST", "127.0.0.1"),
		IMAPPort:              imapPort,
		SMTPHost:              getenv("SMTP_HOST", "127.0.0.1"),
		SMTPPort:              smtpPort,
		BetazenPanelURL:       getenv("BETAZEN_PANEL_URL", ""),
		BetazenPanelToken:     getenv("BETAZEN_PANEL_TOKEN", ""),
		FCMCredentialsFile:    getenv("FCM_CREDENTIALS_FILE", ""),
		FCMProjectID:          getenv("FCM_PROJECT_ID", ""),
		// Web Push (VAPID). Left blank by default: when unset, the mail-suite
		// auto-generates a keypair on first boot and persists it in Mongo, so
		// push works out of the box on every server (new or existing) with no
		// secret ever committed to the repo. Set these to pin/rotate a specific
		// pair (e.g. one shared across a fleet).
		VapidPublic:  getenv("VAPID_PUBLIC", ""),
		VapidPrivate: getenv("VAPID_PRIVATE", ""),
		VapidSubject: getenv("VAPID_SUBJECT", "mailto:betazeninfotech@gmail.com"),
		WebAuthnRPID:          getenv("WEBAUTHN_RPID", "localhost"),
		WebAuthnRPDisplayName: getenv("WEBAUTHN_RP_NAME", "Betazen Mail"),
		WebAuthnOrigins:       splitCSV(getenv("WEBAUTHN_ORIGINS", "http://localhost:5173,http://localhost:9090")),
		OAuthIssuer:           getenv("OAUTH_ISSUER", "http://localhost:9090"),
	}
	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func requireEnv(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			seg := s[start:i]
			if seg != "" {
				out = append(out, seg)
			}
			start = i + 1
		}
	}
	return out
}
