package config

import (
	"os"
	"strconv"
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
	_ = godotenv.Load()

	return &Config{
		AppEnv:   getEnv("APP_ENV", "development"),
		LogLevel: getEnv("LOG_LEVEL", "debug"),

		MongoURI:    getEnv("MONGO_URI", "mongodb://localhost:27017"),
		MongoDBName: getEnv("MONGO_DB_NAME", "serverpanel"),

		JWTSecret:        getEnv("JWT_SECRET", "dev-secret-change-in-production"),
		JWTAccessExpiry:  parseDuration(getEnv("JWT_ACCESS_EXPIRY", "15m")),
		JWTRefreshExpiry: parseDuration(getEnv("JWT_REFRESH_EXPIRY", "168h")),

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

		MailHostname: getEnv("MAIL_HOSTNAME", "mail.localhost"),

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

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 15 * time.Minute
	}
	return d
}
