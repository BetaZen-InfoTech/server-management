package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type WordPressService struct {
	db *mongo.Database
}

func NewWordPressService(db *mongo.Database) *WordPressService {
	return &WordPressService{db: db}
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// wpInstallPath returns the filesystem path for a WordPress installation.
func wpInstallPath(user, domain, path string) string {
	return fmt.Sprintf("/home/%s/domains/%s/public_html%s", user, domain, path)
}

// List returns all WordPress installations managed by the server.
func (s *WordPressService) List(ctx context.Context) ([]models.WordPress, error) {
	col := s.db.Collection(database.ColWordPress)
	cursor, err := col.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var installs []models.WordPress
	if err := cursor.All(ctx, &installs); err != nil {
		return nil, err
	}
	if installs == nil {
		installs = []models.WordPress{}
	}
	return installs, nil
}

// GetByID retrieves a single WordPress installation by its ID.
func (s *WordPressService) GetByID(ctx context.Context, id string) (*models.WordPress, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid WordPress ID")
	}
	var wp models.WordPress
	if err := s.db.Collection(database.ColWordPress).FindOne(ctx, bson.M{"_id": oid}).Decode(&wp); err != nil {
		return nil, err
	}
	return &wp, nil
}

// Install downloads and sets up a new WordPress installation.
func (s *WordPressService) Install(ctx context.Context, req *models.InstallWordPressRequest) (*models.WordPress, error) {
	// Normalize path: ensure it starts with / (or is empty for root)
	path := strings.TrimSpace(req.Path)
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimRight(path, "/")

	// 1. Look up domain to get the system user
	var domain models.Domain
	if err := s.db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": req.Domain}).Decode(&domain); err != nil {
		return nil, fmt.Errorf("domain '%s' not found — create the domain first", req.Domain)
	}
	user := domain.User

	// 2. Check for conflicts: same domain+path already has WordPress
	conflict := s.db.Collection(database.ColWordPress).FindOne(ctx, bson.M{
		"domain": req.Domain,
		"path":   path,
	})
	if conflict.Err() == nil {
		if path == "" {
			return nil, fmt.Errorf("WordPress is already installed on %s (document root)", req.Domain)
		}
		return nil, fmt.Errorf("WordPress is already installed on %s%s", req.Domain, path)
	}

	// 3. Auto-generate DB name, user, and password
	// Sanitize domain for use in DB identifiers (replace dots/hyphens with underscores)
	sanitized := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(req.Domain, "_")
	suffix := randomHex(4)
	dbName := fmt.Sprintf("%s_wp_%s", user, suffix)
	dbUser := dbName
	// MySQL username max 32 chars
	if len(dbUser) > 32 {
		dbUser = dbUser[:32]
	}
	_ = sanitized // used for readability; suffix ensures uniqueness
	dbPass := randomHex(16)
	dbHost := "localhost"

	// 4. Create MySQL database and user
	if err := agent.CreateMySQLDatabase(ctx, dbName); err != nil {
		return nil, fmt.Errorf("failed to create MySQL database: %w", err)
	}
	if err := agent.CreateMySQLUser(ctx, dbName, dbUser, dbPass, dbHost); err != nil {
		// Clean up the database we just created
		agent.DropMySQLDatabase(ctx, dbName)
		return nil, fmt.Errorf("failed to create MySQL user: %w", err)
	}

	// 5. Build site URLs (use HTTPS if SSL is active)
	scheme := "http"
	if domain.SSLActive {
		scheme = "https"
	}
	siteURL := fmt.Sprintf("%s://%s%s", scheme, req.Domain, path)
	adminURL := fmt.Sprintf("%s://%s%s/wp-admin", scheme, req.Domain, path)

	// 6. Install WordPress via agent (WP-CLI)
	if err := agent.InstallWordPress(ctx, user, req.Domain, path, dbName, dbUser, dbPass, dbHost, siteURL, req.SiteTitle, req.AdminUser, req.AdminPass, req.AdminEmail); err != nil {
		// Clean up MySQL on failure
		agent.DropMySQLUser(ctx, dbUser, dbHost)
		agent.DropMySQLDatabase(ctx, dbName)
		return nil, fmt.Errorf("failed to install WordPress: %w", err)
	}

	// 7. Get installed version
	wpPath := wpInstallPath(user, req.Domain, path)
	version := "unknown"
	if output, err := agent.WPCLICommand(ctx, user, wpPath, "core version"); err == nil {
		version = strings.TrimSpace(output)
	}

	now := time.Now()
	wp := models.WordPress{
		Domain:     req.Domain,
		User:       user,
		Path:       path,
		Version:    version,
		DBName:     dbName,
		DBUser:     dbUser,
		DBPass:     dbPass,
		DBHost:     dbHost,
		SiteURL:    siteURL,
		AdminURL:   adminURL,
		Multisite:  req.Multisite,
		AutoUpdate: req.AutoUpdate,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	result, err := s.db.Collection(database.ColWordPress).InsertOne(ctx, wp)
	if err != nil {
		return nil, err
	}
	wp.ID = result.InsertedID.(primitive.ObjectID)

	// 8. Record the database in the databases collection so it shows in the DB manager
	s.db.Collection(database.ColDatabases).InsertOne(ctx, models.Database{
		DBName:           dbName,
		Type:             "mysql",
		Username:         dbUser,
		Password:         dbPass,
		Domain:           req.Domain,
		Host:             dbHost,
		Port:             3306,
		ConnectionString: fmt.Sprintf("mysql://%s:%s@localhost:3306/%s", dbUser, dbPass, dbName),
		CreatedAt:        now,
		UpdatedAt:        now,
	})

	return &wp, nil
}

// CheckConflict returns true with a message if a WordPress install already exists at the given domain+path.
func (s *WordPressService) CheckConflict(ctx context.Context, domain, path string) (bool, string) {
	path = strings.TrimSpace(path)
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimRight(path, "/")

	err := s.db.Collection(database.ColWordPress).FindOne(ctx, bson.M{
		"domain": domain,
		"path":   path,
	}).Err()
	if err == nil {
		if path == "" {
			return true, fmt.Sprintf("WordPress is already installed on %s (document root)", domain)
		}
		return true, fmt.Sprintf("WordPress is already installed on %s%s", domain, path)
	}
	return false, ""
}

// Delete removes a WordPress installation and optionally its database.
func (s *WordPressService) Delete(ctx context.Context, id string) error {
	wp, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Remove files
	wpPath := wpInstallPath(wp.User, wp.Domain, wp.Path)
	agent.RunCommand(ctx, "rm", "-rf", wpPath)

	oid, _ := primitive.ObjectIDFromHex(id)
	_, err = s.db.Collection(database.ColWordPress).DeleteOne(ctx, bson.M{"_id": oid})
	return err
}

// Update upgrades WordPress core to the latest version.
func (s *WordPressService) Update(ctx context.Context, id string) error {
	wp, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	wpPath := wpInstallPath(wp.User, wp.Domain, wp.Path)
	if _, err := agent.WPCLICommand(ctx, wp.User, wpPath, "core update"); err != nil {
		return fmt.Errorf("failed to update WordPress: %w", err)
	}

	// Get new version
	newVersion := wp.Version
	if output, err := agent.WPCLICommand(ctx, wp.User, wpPath, "core version"); err == nil {
		newVersion = strings.TrimSpace(output)
	}

	_, err = s.db.Collection(database.ColWordPress).UpdateOne(ctx,
		bson.M{"_id": wp.ID},
		bson.M{"$set": bson.M{"version": newVersion, "updated_at": time.Now()}},
	)
	return err
}

// SecurityScan performs a security audit on a WordPress installation.
func (s *WordPressService) SecurityScan(ctx context.Context, id string) (*models.WPSecurityScan, error) {
	wp, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	wpPath := wpInstallPath(wp.User, wp.Domain, wp.Path)
	scanResult, err := agent.WPSecurityScan(ctx, wp.User, wpPath)
	if err != nil {
		return nil, fmt.Errorf("security scan failed: %w", err)
	}

	scan := &models.WPSecurityScan{
		OverallStatus: "good",
		ScannedAt:     time.Now(),
	}

	// Core integrity check
	coreStatus := "pass"
	coreMsg := "Core files are intact"
	if integrity, ok := scanResult["core_integrity"].(string); ok {
		if strings.Contains(integrity, "error") || strings.Contains(integrity, "FAILED") {
			coreStatus = "fail"
			coreMsg = "Core file integrity check failed"
			scan.OverallStatus = "warning"
		}
	}
	scan.Checks = append(scan.Checks, models.WPSecurityCheck{
		Name: "Core Integrity", Status: coreStatus, Message: coreMsg,
	})

	// Outdated plugins check
	pluginStatus := "pass"
	pluginMsg := "All plugins are up to date"
	if outdated, ok := scanResult["outdated_plugins"].(string); ok && outdated != "[]" && outdated != "" {
		pluginStatus = "warning"
		pluginMsg = "Some plugins have updates available"
		scan.OverallStatus = "warning"
	}
	scan.Checks = append(scan.Checks, models.WPSecurityCheck{
		Name: "Plugin Updates", Status: pluginStatus, Message: pluginMsg,
	})

	// Debug mode check
	debugStatus := "pass"
	debugMsg := "Debug mode is disabled"
	if wp.DebugMode {
		debugStatus = "warning"
		debugMsg = "Debug mode is enabled in production"
		scan.OverallStatus = "warning"
	}
	scan.Checks = append(scan.Checks, models.WPSecurityCheck{
		Name: "Debug Mode", Status: debugStatus, Message: debugMsg,
	})

	return scan, nil
}

// ListPlugins returns all plugins installed in a WordPress installation.
func (s *WordPressService) ListPlugins(ctx context.Context, id string) ([]models.WPPlugin, error) {
	wp, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	wpPath := wpInstallPath(wp.User, wp.Domain, wp.Path)
	output, err := agent.WPCLICommand(ctx, wp.User, wpPath, "plugin list --format=json")
	if err != nil {
		return nil, fmt.Errorf("failed to list plugins: %w", err)
	}

	var plugins []models.WPPlugin
	if err := json.Unmarshal([]byte(output), &plugins); err != nil {
		return nil, fmt.Errorf("failed to parse plugin list: %w", err)
	}
	if plugins == nil {
		plugins = []models.WPPlugin{}
	}
	return plugins, nil
}

// InstallPlugin installs a plugin by slug into a WordPress installation.
func (s *WordPressService) InstallPlugin(ctx context.Context, id string, slug string) error {
	wp, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	wpPath := wpInstallPath(wp.User, wp.Domain, wp.Path)
	cmd := fmt.Sprintf("plugin install %s --activate", slug)
	if _, err := agent.WPCLICommand(ctx, wp.User, wpPath, cmd); err != nil {
		return fmt.Errorf("failed to install plugin: %w", err)
	}
	return nil
}

// AutoLogin generates a temporary auto-login URL for WordPress admin.
// It creates a one-time login token by writing a temporary PHP script.
func (s *WordPressService) AutoLogin(ctx context.Context, id string) (string, error) {
	wp, err := s.GetByID(ctx, id)
	if err != nil {
		return "", err
	}

	// Look up domain to check SSL
	var domain models.Domain
	if err := s.db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": wp.Domain}).Decode(&domain); err != nil {
		return "", fmt.Errorf("domain not found: %w", err)
	}

	wpPath := wpInstallPath(wp.User, wp.Domain, wp.Path)

	// Generate a random token for the auto-login link
	token := randomHex(32)

	// Create a temporary PHP auto-login script in wp-content/ (public_html is often
	// locked to r-x for hardening; wp-content is writable by the domain user).
	// It logs in as the first admin user and self-deletes after use or after 60 seconds.
	phpScript := fmt.Sprintf(`<?php
// Auto-login script - self-destructs after use or 60s
if (time() - filemtime(__FILE__) > 60) { @unlink(__FILE__); die('Link expired'); }
if (!isset($_GET['token']) || !hash_equals('%s', $_GET['token'])) { die('Invalid token'); }
@unlink(__FILE__); // Self-delete immediately on use
define('ABSPATH', dirname(dirname(__FILE__)) . '/');
require_once(ABSPATH . 'wp-load.php');
$users = get_users(array('role' => 'administrator', 'number' => 1));
if (empty($users)) { die('No admin user found'); }
wp_set_auth_cookie($users[0]->ID, true, is_ssl());
wp_set_current_user($users[0]->ID);
wp_redirect(admin_url());
exit;
`, token)

	// Write the script as the domain user into wp-content (writable even when
	// public_html itself is read-only).
	scriptName := fmt.Sprintf("wp-auto-login-%s.php", token[:8])
	scriptPath := fmt.Sprintf("%s/wp-content/%s", wpPath, scriptName)
	writeCmd := fmt.Sprintf("cat > '%s' << 'PHPEOF'\n%s\nPHPEOF", scriptPath, phpScript)
	if _, err := agent.RunCommandAsUser(ctx, wp.User, writeCmd); err != nil {
		return "", fmt.Errorf("failed to create auto-login script: %w", err)
	}

	scheme := "http"
	if domain.SSLActive {
		scheme = "https"
	}

	loginURL := fmt.Sprintf("%s://%s%s/wp-content/%s?token=%s", scheme, wp.Domain, wp.Path, scriptName, token)
	return loginURL, nil
}

// ListUsers returns WordPress users for an installation.
func (s *WordPressService) ListUsers(ctx context.Context, id string) ([]map[string]interface{}, error) {
	wp, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	wpPath := wpInstallPath(wp.User, wp.Domain, wp.Path)
	output, err := agent.WPCLICommand(ctx, wp.User, wpPath, "user list --format=json --fields=ID,user_login,user_email,display_name,roles")
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}

	var users []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &users); err != nil {
		return nil, fmt.Errorf("failed to parse user list: %w", err)
	}
	if users == nil {
		users = []map[string]interface{}{}
	}
	return users, nil
}

// CreateUser creates a new WordPress user.
func (s *WordPressService) CreateUser(ctx context.Context, id string, username, email, password, role string) error {
	wp, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	wpPath := wpInstallPath(wp.User, wp.Domain, wp.Path)
	cmd := fmt.Sprintf("user create '%s' '%s' --user_pass='%s' --role='%s'", username, email, password, role)
	if _, err := agent.WPCLICommand(ctx, wp.User, wpPath, cmd); err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	return nil
}

// DeleteUser deletes a WordPress user by ID, reassigning their content to user ID 1.
func (s *WordPressService) DeleteUser(ctx context.Context, id string, wpUserID string) error {
	wp, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	wpPath := wpInstallPath(wp.User, wp.Domain, wp.Path)
	cmd := fmt.Sprintf("user delete %s --reassign=1 --yes", wpUserID)
	if _, err := agent.WPCLICommand(ctx, wp.User, wpPath, cmd); err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}
	return nil
}

// UpdateUserRole changes a WordPress user's role.
func (s *WordPressService) UpdateUserRole(ctx context.Context, id string, wpUserID string, role string) error {
	wp, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	wpPath := wpInstallPath(wp.User, wp.Domain, wp.Path)
	cmd := fmt.Sprintf("user set-role %s '%s'", wpUserID, role)
	if _, err := agent.WPCLICommand(ctx, wp.User, wpPath, cmd); err != nil {
		return fmt.Errorf("failed to update user role: %w", err)
	}
	return nil
}

// ToggleMaintenance enables or disables maintenance mode on a WordPress installation.
func (s *WordPressService) ToggleMaintenance(ctx context.Context, id string, enabled bool) error {
	wp, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	wpPath := wpInstallPath(wp.User, wp.Domain, wp.Path)
	action := "deactivate"
	if enabled {
		action = "activate"
	}

	if _, err := agent.WPCLICommand(ctx, wp.User, wpPath, "maintenance-mode "+action); err != nil {
		return fmt.Errorf("failed to toggle maintenance mode: %w", err)
	}

	_, err = s.db.Collection(database.ColWordPress).UpdateOne(ctx,
		bson.M{"_id": wp.ID},
		bson.M{"$set": bson.M{"maintenance_mode": enabled, "updated_at": time.Now()}},
	)
	return err
}
