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
	"go.mongodb.org/mongo-driver/mongo/options"
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

// applyAutoUpdateConfig sets the WP_AUTO_UPDATE_CORE and AUTOMATIC_UPDATER_DISABLED
// constants in wp-config.php via wp-cli. Enabled=true turns on minor core updates.
//
// IMPORTANT: wp-cli's --raw flag inserts the value verbatim, so it MUST only be
// used for booleans (true/false). Passing --raw with the string "minor" produced
//
//	define( 'WP_AUTO_UPDATE_CORE', minor );
//
// which PHP parses as an undefined constant and aborts every request with a
// fatal error → every WP site on the panel returned HTTP 500. Now we omit
// --raw for the string values so wp-cli quotes them properly:
//
//	define( 'WP_AUTO_UPDATE_CORE', 'minor' );
func applyAutoUpdateConfig(ctx context.Context, user, wpPath string, enabled bool) error {
	if enabled {
		// String value — let wp-cli quote it.
		if _, err := agent.WPCLICommand(ctx, user, wpPath,
			"config set WP_AUTO_UPDATE_CORE minor"); err != nil {
			return fmt.Errorf("wp config set WP_AUTO_UPDATE_CORE: %w", err)
		}
		// Boolean — needs --raw.
		if _, err := agent.WPCLICommand(ctx, user, wpPath,
			"config set AUTOMATIC_UPDATER_DISABLED false --raw"); err != nil {
			return fmt.Errorf("wp config set AUTOMATIC_UPDATER_DISABLED: %w", err)
		}
		return nil
	}
	// Disabled: both values are booleans.
	if _, err := agent.WPCLICommand(ctx, user, wpPath,
		"config set WP_AUTO_UPDATE_CORE false --raw"); err != nil {
		return fmt.Errorf("wp config set WP_AUTO_UPDATE_CORE: %w", err)
	}
	if _, err := agent.WPCLICommand(ctx, user, wpPath,
		"config set AUTOMATIC_UPDATER_DISABLED true --raw"); err != nil {
		return fmt.Errorf("wp config set AUTOMATIC_UPDATER_DISABLED: %w", err)
	}
	return nil
}

// readAutoUpdateConfig reads the current WP_AUTO_UPDATE_CORE value from wp-config.php
// and returns true if it enables any form of core auto-updates.
func readAutoUpdateConfig(ctx context.Context, user, wpPath string) bool {
	out, err := agent.WPCLICommand(ctx, user, wpPath, "config get WP_AUTO_UPDATE_CORE --format=json")
	if err != nil {
		return false
	}
	v := strings.TrimSpace(out)
	v = strings.Trim(v, "\"")
	// WordPress treats true, "minor", or "major" as enabled; false/empty as disabled.
	return v == "true" || v == "minor" || v == "major"
}

// List returns all WordPress installations managed by the server.
func (s *WordPressService) List(ctx context.Context) ([]models.WordPress, error) {
	col := s.db.Collection(database.ColWordPress)
	filter := bson.M{}
	if scope := GetCallerScope(ctx); scope != nil {
		filter = scope.ApplyTo(ctx, s.db, "user", filter)
	}
	cursor, err := col.Find(ctx, filter)
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
// Every per-install mutation in this service (Delete, Update, plugin
// ops, toggles, AutoLogin, security scan, …) funnels through here,
// so the caller-scope check here is the single chokepoint that
// prevents a vendor from touching another tenant's WP install by
// guessing an ObjectID. Without this, the whole /api/v1/cpanel/wordpress
// group leaks cross-tenant because the per-id routes aren't
// individually gated on domain ownership. Pattern matches ssl_service
// and database_service's id lookups after the same fix.
func (s *WordPressService) GetByID(ctx context.Context, id string) (*models.WordPress, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid WordPress ID")
	}
	filter := bson.M{"_id": oid}
	if scope := GetCallerScope(ctx); scope != nil {
		filter = scope.ApplyTo(ctx, s.db, "user", filter)
	}
	var wp models.WordPress
	if err := s.db.Collection(database.ColWordPress).FindOne(ctx, filter).Decode(&wp); err != nil {
		return nil, fmt.Errorf("WordPress install not found")
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

	// 3. Resolve MySQL credentials. Three modes — "auto" (default) is
	// what WordPress has always done; "existing" and "manual" let an
	// operator bring their own DB per the upgraded install form.
	var (
		dbName, dbUser, dbPass, dbHost string
		panelCreatedDB                 bool // true if we CREATE-ed the db/user, so cleanup paths run
	)
	mode := strings.ToLower(strings.TrimSpace(req.DBMode))
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "auto":
		// Sanitize domain for readability; suffix guarantees uniqueness
		// across multi-install-per-domain setups (e.g. /blog + /shop).
		_ = regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(req.Domain, "_")
		suffix := randomHex(4)
		dbName = fmt.Sprintf("%s_wp_%s", user, suffix)
		dbUser = dbName
		if len(dbUser) > 32 { // MySQL 5.7+ caps usernames at 32
			dbUser = dbUser[:32]
		}
		dbPass = randomHex(16)
		dbHost = "localhost"
		if err := agent.CreateMySQLDatabase(ctx, dbName); err != nil {
			return nil, fmt.Errorf("failed to create MySQL database: %w", err)
		}
		if err := agent.CreateMySQLUser(ctx, dbName, dbUser, dbPass, dbHost); err != nil {
			agent.DropMySQLDatabase(ctx, dbName)
			return nil, fmt.Errorf("failed to create MySQL user: %w", err)
		}
		panelCreatedDB = true

	case "existing":
		// Use credentials the operator already created. No CREATE runs,
		// which means WP-CLI must be able to GRANT on the existing DB
		// via the supplied user — we leave that to the operator since
		// the expected flow is "make the DB in Databases, then install
		// WP on top".
		dbName = strings.TrimSpace(req.DBName)
		dbUser = strings.TrimSpace(req.DBUser)
		dbPass = req.DBPass
		dbHost = strings.TrimSpace(req.DBHost)
		if dbHost == "" {
			dbHost = "localhost"
		}
		if dbName == "" || dbUser == "" || dbPass == "" {
			return nil, fmt.Errorf("db_mode=existing requires db_name, db_user, and db_pass")
		}

	case "manual":
		// Operator typed specific credentials (useful when importing an
		// existing WP dump). Panel creates the DB + user with those
		// exact names, same as auto but without the generator.
		dbName = strings.TrimSpace(req.DBName)
		dbUser = strings.TrimSpace(req.DBUser)
		dbPass = req.DBPass
		dbHost = strings.TrimSpace(req.DBHost)
		if dbHost == "" {
			dbHost = "localhost"
		}
		if dbName == "" || dbUser == "" || dbPass == "" {
			return nil, fmt.Errorf("db_mode=manual requires db_name, db_user, and db_pass")
		}
		if len(dbUser) > 32 {
			return nil, fmt.Errorf("db_user is too long (MySQL cap is 32 chars)")
		}
		if err := agent.CreateMySQLDatabase(ctx, dbName); err != nil {
			return nil, fmt.Errorf("failed to create MySQL database %q: %w", dbName, err)
		}
		if err := agent.CreateMySQLUser(ctx, dbName, dbUser, dbPass, dbHost); err != nil {
			agent.DropMySQLDatabase(ctx, dbName)
			return nil, fmt.Errorf("failed to create MySQL user %q: %w", dbUser, err)
		}
		panelCreatedDB = true

	default:
		return nil, fmt.Errorf("unknown db_mode %q — use auto, existing, or manual", mode)
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
		// Only roll back the MySQL CREATE when we were the one who did
		// it. db_mode=existing uses an operator-owned DB/user — dropping
		// it on install failure would nuke unrelated data.
		if panelCreatedDB {
			agent.DropMySQLUser(ctx, dbUser, dbHost)
			agent.DropMySQLDatabase(ctx, dbName)
		}
		return nil, fmt.Errorf("failed to install WordPress: %w", err)
	}

	// 7. Get installed version
	wpPath := wpInstallPath(user, req.Domain, path)
	version := "unknown"
	if output, err := agent.WPCLICommand(ctx, user, wpPath, "core version"); err == nil {
		version = strings.TrimSpace(output)
	}

	// 7a. Apply auto-update configuration to wp-config.php if requested
	if err := applyAutoUpdateConfig(ctx, user, wpPath, req.AutoUpdate); err != nil {
		// Non-fatal: log via error return wrapping but keep the install
		return nil, fmt.Errorf("WordPress installed but failed to apply auto-update setting: %w", err)
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

	// Normalise perms first so the script we're about to write is readable by
	// www-data and the domain user can actually write it. Also covers the case
	// where the site was just restored from backup with wrong perms.
	if err := agent.EnsureWebPerms(ctx, wp.User, wp.Domain); err != nil {
		return "", fmt.Errorf("failed to normalise web perms: %w", err)
	}

	// Generate a random token for the auto-login link
	token := randomHex(32)

	// Auto-login loader. Lives at the WordPress root (next to wp-load.php) so
	// ABSPATH is just dirname(__FILE__) — and so it isn't blocked by security
	// rules that disable PHP execution under wp-content/.
	//
	// We do NOT unlink before require: a fatal in wp-load would otherwise
	// produce a silent 500 with no way to debug. Unlink runs only on the
	// successful path, and a separate expiry guard removes stale loaders.
	phpScript := fmt.Sprintf(`<?php
@ini_set('display_errors', '1');
@error_reporting(E_ALL);
if (time() - filemtime(__FILE__) > 300) { @unlink(__FILE__); http_response_code(410); die('Link expired'); }
if (!isset($_GET['token']) || !hash_equals('%s', $_GET['token'])) { http_response_code(403); die('Invalid token'); }
define('ABSPATH', dirname(__FILE__) . '/');
if (!file_exists(ABSPATH . 'wp-load.php')) { http_response_code(500); die('wp-load.php not found at ' . ABSPATH); }
require_once(ABSPATH . 'wp-load.php');
$users = get_users(array('role' => 'administrator', 'number' => 1));
if (empty($users)) { die('No admin user found'); }
wp_set_auth_cookie($users[0]->ID, true, is_ssl());
wp_set_current_user($users[0]->ID);
@unlink(__FILE__);
wp_redirect(admin_url());
exit;
`, token)

	// Write the script as the domain user into public_html (next to wp-load.php).
	scriptName := fmt.Sprintf("wp-auto-login-%s.php", token[:8])
	scriptPath := fmt.Sprintf("%s/%s", wpPath, scriptName)
	writeCmd := fmt.Sprintf("cat > '%s' << 'PHPEOF'\n%s\nPHPEOF", scriptPath, phpScript)
	if _, err := agent.RunCommandAsUser(ctx, wp.User, writeCmd); err != nil {
		return "", fmt.Errorf("failed to create auto-login script: %w", err)
	}
	// Belt-and-braces: chmod the script 644 in case the user umask is restrictive.
	agent.RunCommand(ctx, "chmod", "644", scriptPath)

	scheme := "http"
	if domain.SSLActive {
		scheme = "https"
	}

	loginURL := fmt.Sprintf("%s://%s%s/%s?token=%s", scheme, wp.Domain, wp.Path, scriptName, token)
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

// ToggleAutoUpdate enables or disables WordPress core auto-updates by setting
// the WP_AUTO_UPDATE_CORE constant in wp-config.php and persisting the flag.
func (s *WordPressService) ToggleAutoUpdate(ctx context.Context, id string, enabled bool) error {
	wp, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	wpPath := wpInstallPath(wp.User, wp.Domain, wp.Path)
	if err := applyAutoUpdateConfig(ctx, wp.User, wpPath, enabled); err != nil {
		return fmt.Errorf("failed to toggle auto-update: %w", err)
	}

	_, err = s.db.Collection(database.ColWordPress).UpdateOne(ctx,
		bson.M{"_id": wp.ID},
		bson.M{"$set": bson.M{"auto_update": enabled, "updated_at": time.Now()}},
	)
	return err
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

// RescanUser walks the filesystem under a given user's home directory, finds
// every wp-config.php, and upserts a matching WordPress record into the
// `wordpress` collection. It reads the current auto-update configuration from
// each wp-config so that the panel flag matches reality after a restore or
// transfer overwrites on-disk files. If user is empty, all users are scanned.
func (s *WordPressService) RescanUser(ctx context.Context, user string) (int, error) {
	// Locate wp-config.php files. We look inside /home/<user>/domains/*/public_html*
	// and up to two subdirectories deep (handles installs at document root or in a
	// subfolder like /blog).
	var root string
	if user == "" {
		root = "/home"
	} else {
		root = "/home/" + user
	}
	findRes, err := agent.RunCommand(ctx, "find", root, "-maxdepth", "6",
		"-type", "f", "-name", "wp-config.php", "-not", "-path", "*/wp-content/*")
	if err != nil {
		return 0, fmt.Errorf("find wp-config.php: %w", err)
	}

	count := 0
	for _, line := range strings.Split(strings.TrimSpace(findRes.Output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// line looks like: /home/<user>/domains/<domain>/public_html[/<subpath>]/wp-config.php
		wpDir := strings.TrimSuffix(line, "/wp-config.php")
		parts := strings.SplitN(wpDir, "/", 7)
		if len(parts) < 6 || parts[1] != "home" || parts[3] != "domains" || !strings.HasPrefix(parts[5], "public_html") {
			continue
		}
		foundUser := parts[2]
		domain := parts[4]
		subPath := ""
		if len(parts) == 7 {
			// everything after public_html is the install subpath
			tail := parts[6]
			subPath = strings.TrimPrefix(tail, "/")
			if subPath != "" {
				subPath = "/" + subPath
			}
		}

		// Pull metadata via wp-cli
		version := "unknown"
		if out, err := agent.WPCLICommand(ctx, foundUser, wpDir, "core version"); err == nil {
			version = strings.TrimSpace(out)
		}
		autoUpdate := readAutoUpdateConfig(ctx, foundUser, wpDir)
		dbName, _ := agent.WPCLICommand(ctx, foundUser, wpDir, "config get DB_NAME")
		dbUser, _ := agent.WPCLICommand(ctx, foundUser, wpDir, "config get DB_USER")
		dbHost, _ := agent.WPCLICommand(ctx, foundUser, wpDir, "config get DB_HOST")
		siteURL, _ := agent.WPCLICommand(ctx, foundUser, wpDir, "option get siteurl")
		siteURL = strings.TrimSpace(siteURL)
		if siteURL == "" {
			scheme := "http"
			var dom models.Domain
			if err := s.db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": domain}).Decode(&dom); err == nil && dom.SSLActive {
				scheme = "https"
			}
			siteURL = fmt.Sprintf("%s://%s%s", scheme, domain, subPath)
		}

		now := time.Now()
		filter := bson.M{"domain": domain, "path": subPath}
		update := bson.M{
			"$set": bson.M{
				"domain":      domain,
				"user":        foundUser,
				"path":        subPath,
				"version":     version,
				"db_name":     strings.TrimSpace(dbName),
				"db_user":     strings.TrimSpace(dbUser),
				"db_host":     strings.TrimSpace(dbHost),
				"site_url":    siteURL,
				"admin_url":   strings.TrimRight(siteURL, "/") + "/wp-admin",
				"auto_update": autoUpdate,
				"updated_at":  now,
			},
			"$setOnInsert": bson.M{"created_at": now},
		}
		if _, err := s.db.Collection(database.ColWordPress).UpdateOne(ctx, filter, update,
			options.Update().SetUpsert(true)); err == nil {
			count++
		}
	}
	return count, nil
}
