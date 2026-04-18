package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AppEnv   string
	LogLevel string

	// MongoDB
	MongoURI    string
	MongoDBName string

	// JWT
	JWTSecret       string
	JWTAccessExpiry time.Duration
	JWTRefreshExpiry time.Duration

	// Server
	Domain     string
	ServerPort string
	TLSCert    string
	TLSKey     string

	// Agent
	AgentPort    string
	AgentAPIKey  string
	AgentTLSCert string
	AgentTLSKey  string

	// GitHub
	GitHubClientID     string
	GitHubClientSecret string
	GitHubWebhookSecret string

	// Server IP (for DNS records)
	ServerIP string

	// Deploy Software / Project feature
	AppEncryptionKey     string // 32 bytes, hex or base64. Required in production.
	PublicWebhookBaseURL string // Public URL for GitHub webhooks, e.g. https://panel.example.com

	// Email
	MailHostname string

	// Backup
	BackupDir          string
	BackupEncryptionKey string

	// Rate limiting
	RateLimitWHM    int
	RateLimitCPanel int
}

func Load() *Config {
	// Try loading .env from multiple locations
	envPaths := []string{
		".env",                         // current working directory
		"/opt/serverpanel/.env",        // production default
	}

	// Also check next to the executable
	if exe, err := os.Executable(); err == nil {
		envPaths = append(envPaths, filepath.Join(filepath.Dir(exe), ".env"))
	}

	loaded := false
	for _, p := range envPaths {
		if err := godotenv.Load(p); err == nil {
			fmt.Printf("[config] Loaded .env from %s\n", p)
			loaded = true
			break
		}
	}
	if !loaded {
		fmt.Println("[config] No .env file found, using environment variables / defaults")
	}

	return &Config{
		AppEnv:   getEnv("APP_ENV", "development"),
		LogLevel: getEnv("LOG_LEVEL", "debug"),

		MongoURI:    getEnv("MONGO_URI", "mongodb://localhost:27017"),
		MongoDBName: getEnv("MONGO_DB_NAME", "serverpanel"),

		JWTSecret: getEnv("JWT_SECRET", "dev-secret-change-in-production"),
		// 4h default access TTL — long enough that an operator on the WHM
		// dashboard doesn't get logged out mid-task (the old 15m default
		// re-authed users every 15 minutes which surfaced as "after some
		// time my account logs out"), short enough that CSRF / lost-laptop
		// risk stays bounded. Refresh is still 7d / 30d so the panel can
		// silently extend across long-lived sessions.
		JWTAccessExpiry:  parseDuration(getEnv("JWT_ACCESS_EXPIRY", "4h")),
		JWTRefreshExpiry: parseDuration(getEnv("JWT_REFRESH_EXPIRY", "720h")),

		Domain:     getEnv("DOMAIN", "localhost"),
		ServerPort: getEnv("SERVER_PORT", "8080"),
		TLSCert:    getEnv("TLS_CERT", ""),
		TLSKey:     getEnv("TLS_KEY", ""),

		AgentPort:    getEnv("AGENT_PORT", "8443"),
		AgentAPIKey:  getEnv("AGENT_API_KEY", "dev-agent-key"),
		AgentTLSCert: getEnv("AGENT_TLS_CERT", ""),
		AgentTLSKey:  getEnv("AGENT_TLS_KEY", ""),

		GitHubClientID:      getEnv("GITHUB_CLIENT_ID", ""),
		GitHubClientSecret:  getEnv("GITHUB_CLIENT_SECRET", ""),
		GitHubWebhookSecret: getEnv("GITHUB_WEBHOOK_SECRET", ""),

		ServerIP:     getEnvOrDetectIP("SERVER_IP"),
		MailHostname: getEnv("MAIL_HOSTNAME", "mail.localhost"),

		AppEncryptionKey:     getEnv("APP_ENCRYPTION_KEY", ""),
		PublicWebhookBaseURL: getEnv("PUBLIC_WEBHOOK_BASE_URL", ""),

		BackupDir:           getEnv("BACKUP_DIR", "./tmp/backups"),
		BackupEncryptionKey: getEnv("BACKUP_ENCRYPTION_KEY", ""),

		RateLimitWHM:    getEnvInt("RATE_LIMIT_WHM", 200),
		RateLimitCPanel: getEnvInt("RATE_LIMIT_CPANEL", 100),
	}
}

func (c *Config) IsProduction() bool {
	return c.AppEnv == "production"
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvOrDetectIP(key string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	// Auto-detect server IP via hostname -I
	out, err := exec.Command("hostname", "-I").Output()
	if err == nil {
		parts := strings.Fields(strings.TrimSpace(string(out)))
		if len(parts) > 0 {
			return parts[0]
		}
	}
	return ""
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 15 * time.Minute
	}
	return d
}
