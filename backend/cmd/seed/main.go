package main

import (
	"context"
	"fmt"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := database.Connect(cfg)
	if err != nil {
		fmt.Printf("Failed to connect to MongoDB: %v\n", err)
		return
	}
	defer database.Disconnect()

	col := db.Collection(database.ColUsers)

	// Demo admin user for WHM
	seedUser(ctx, col, "admin@betazeninfotech.com", "admin123", "Admin", "vendor_owner", []string{
		"domain.view", "domain.create", "domain.manage", "domain.delete",
		"app.view", "app.deploy", "app.manage",
		"database.view", "database.create", "database.manage",
		"email.view", "email.create", "email.manage",
		"dns.view", "dns.create", "dns.manage",
		"ssl.view", "ssl.manage",
		"backup.view", "backup.create", "backup.restore",
		"wordpress.view", "wordpress.manage",
		"firewall.view", "firewall.manage",
		"software.view", "software.manage",
		"monitor.view",
		"log.view",
		"cron.view", "cron.manage",
		"file.view", "file.manage",
		"ssh.view", "ssh.manage",
		"process.view", "process.manage",
		"server.view", "server.manage",
		"notification.view", "notification.manage",
		"audit.view",
		"config.view", "config.manage",
		"deploy.view", "deploy.manage",
		"user.view", "user.create", "user.manage",
		"package.view", "package.create", "package.manage", "package.delete",
	})

	// Default hosting package
	seedDefaultPackage(ctx, db)

	// Demo customer user for cPanel
	seedUser(ctx, col, "demo@betazeninfotech.com", "demo123", "Demo User", "customer", []string{
		"domain.view",
		"app.view",
		"database.view",
		"email.view",
		"dns.view",
		"ssl.view",
		"backup.view",
		"wordpress.view",
		"file.view",
		"ssh.view",
		"cron.view",
	})

	fmt.Println("\nSeed completed!")
}

func seedDefaultPackage(ctx context.Context, db *mongo.Database) {
	col := db.Collection("hosting_packages")
	count, _ := col.CountDocuments(ctx, bson.M{"is_default": true})
	if count > 0 {
		fmt.Println("[skip] Default package already exists")
		return
	}

	now := time.Now()
	pkg := bson.M{
		"_id":                       primitive.NewObjectID(),
		"name":                      "Default",
		"created_by":                "system",
		"is_default":                true,
		"disk_quota_mb":             5120,
		"disk_quota_unlimited":      false,
		"bandwidth_mb":              102400,
		"bandwidth_unlimited":       false,
		"max_ftp_accounts":          10,
		"max_ftp_unlimited":         false,
		"max_email_accounts":        50,
		"max_email_unlimited":       false,
		"max_mailing_lists":         5,
		"max_mailing_unlimited":     false,
		"max_databases":             10,
		"max_databases_unlimited":   false,
		"max_subdomains":            20,
		"max_subdomains_unlimited":  false,
		"max_parked_domains":        5,
		"max_parked_unlimited":      false,
		"max_addon_domains":         5,
		"max_addon_unlimited":       false,
		"max_passenger_apps":        5,
		"max_passenger_unlimited":   false,
		"max_hourly_email":          500,
		"max_hourly_email_unlimited": false,
		"max_fail_percent":          30,
		"max_email_quota_mb":        250,
		"max_email_quota_unlimited": false,
		"shell_access":              false,
		"cgi_access":                false,
		"wp_toolkit":                true,
		"account_count":             0,
		"created_at":                now,
		"updated_at":                now,
	}

	_, err := col.InsertOne(ctx, pkg)
	if err != nil {
		fmt.Printf("[error] Failed to create default package: %v\n", err)
		return
	}
	fmt.Println("[created] Default hosting package")
}

func seedUser(ctx context.Context, col *mongo.Collection, email, password, name, role string, perms []string) {
	// Check if user already exists
	count, _ := col.CountDocuments(ctx, bson.M{"email": email})
	if count > 0 {
		fmt.Printf("[skip] User %s already exists\n", email)
		return
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fmt.Printf("[error] Failed to hash password for %s: %v\n", email, err)
		return
	}

	now := time.Now()
	user := bson.M{
		"_id":                primitive.NewObjectID(),
		"email":              email,
		"password":           string(hash),
		"name":               name,
		"role":               role,
		"permissions":        perms,
		"domains":            []string{},
		"is_active":          true,
		"two_factor_enabled": false,
		"two_factor_secret":  "",
		"recovery_codes":     []string{},
		"refresh_token":      "",
		"failed_logins":      0,
		"locked_until":       nil,
		"last_login":         nil,
		"created_at":         now,
		"updated_at":         now,
	}

	_, err = col.InsertOne(ctx, user)
	if err != nil {
		fmt.Printf("[error] Failed to create user %s: %v\n", email, err)
		return
	}

	fmt.Printf("[created] %s (%s) — password: %s\n", email, role, password)
}
