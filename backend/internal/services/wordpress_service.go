package services

import (
	"context"
	"encoding/json"
	"fmt"
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
	dbHost := req.DBHost
	if dbHost == "" {
		dbHost = "localhost"
	}

	siteURL := fmt.Sprintf("http://%s%s", req.Domain, req.Path)
	adminURL := fmt.Sprintf("http://%s%s/wp-admin", req.Domain, req.Path)

	if err := agent.InstallWordPress(ctx, req.User, req.Path, req.DBName, req.DBUser, req.DBPass, dbHost, siteURL, req.SiteTitle, req.AdminUser, req.AdminPass, req.AdminEmail); err != nil {
		return nil, fmt.Errorf("failed to install WordPress: %w", err)
	}

	// Get version
	wpPath := fmt.Sprintf("/home/%s/public_html%s", req.User, req.Path)
	version := "unknown"
	if output, err := agent.WPCLICommand(ctx, req.User, wpPath, "core version"); err == nil {
		version = strings.TrimSpace(output)
	}

	wp := models.WordPress{
		Domain:    req.Domain,
		User:      req.User,
		Path:      req.Path,
		Version:   version,
		DBName:    req.DBName,
		DBUser:    req.DBUser,
		DBPass:    req.DBPass,
		DBHost:    dbHost,
		SiteURL:   siteURL,
		AdminURL:  adminURL,
		Multisite: req.Multisite,
		AutoUpdate: req.AutoUpdate,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	result, err := s.db.Collection(database.ColWordPress).InsertOne(ctx, wp)
	if err != nil {
		return nil, err
	}
	wp.ID = result.InsertedID.(primitive.ObjectID)
	return &wp, nil
}

// Delete removes a WordPress installation and optionally its database.
func (s *WordPressService) Delete(ctx context.Context, id string) error {
	wp, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Remove files
	wpPath := fmt.Sprintf("/home/%s/public_html%s", wp.User, wp.Path)
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

	wpPath := fmt.Sprintf("/home/%s/public_html%s", wp.User, wp.Path)
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

	wpPath := fmt.Sprintf("/home/%s/public_html%s", wp.User, wp.Path)
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

	wpPath := fmt.Sprintf("/home/%s/public_html%s", wp.User, wp.Path)
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

	wpPath := fmt.Sprintf("/home/%s/public_html%s", wp.User, wp.Path)
	cmd := fmt.Sprintf("plugin install %s --activate", slug)
	if _, err := agent.WPCLICommand(ctx, wp.User, wpPath, cmd); err != nil {
		return fmt.Errorf("failed to install plugin: %w", err)
	}
	return nil
}

// ToggleMaintenance enables or disables maintenance mode on a WordPress installation.
func (s *WordPressService) ToggleMaintenance(ctx context.Context, id string, enabled bool) error {
	wp, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	wpPath := fmt.Sprintf("/home/%s/public_html%s", wp.User, wp.Path)
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
