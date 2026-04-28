package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type WordPress struct {
	ID              primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Domain          string             `bson:"domain" json:"domain"`
	User            string             `bson:"user" json:"user"`
	Path            string             `bson:"path" json:"path"`
	Version         string             `bson:"version" json:"version"`
	DBName          string             `bson:"db_name" json:"db_name"`
	DBUser          string             `bson:"db_user" json:"db_user"`
	DBPass          string             `bson:"db_pass" json:"-"`
	DBHost          string             `bson:"db_host" json:"db_host"`
	SiteURL         string             `bson:"site_url" json:"site_url"`
	AdminURL        string             `bson:"admin_url" json:"admin_url"`
	Multisite       bool               `bson:"multisite" json:"multisite"`
	AutoUpdate      bool               `bson:"auto_update" json:"auto_update"`
	DebugMode       bool               `bson:"debug_mode" json:"debug_mode"`
	MaintenanceMode bool               `bson:"maintenance_mode" json:"maintenance_mode"`
	DiskUsageMB     float64            `bson:"disk_usage_mb" json:"disk_usage_mb"`
	CreatedAt       time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt       time.Time          `bson:"updated_at" json:"updated_at"`
}

type InstallWordPressRequest struct {
	Domain     string `json:"domain" validate:"required"`
	Path       string `json:"path"`
	SiteTitle  string `json:"site_title" validate:"required"`
	AdminUser  string `json:"admin_user" validate:"required"`
	AdminPass  string `json:"admin_pass" validate:"required,min=8"`
	AdminEmail string `json:"admin_email" validate:"required,email"`
	Multisite  bool   `json:"multisite"`
	AutoUpdate bool   `json:"auto_update"`

	// Version pins the WordPress core release the operator picked in the
	// install wizard ("latest", "6.5", "6.4", …). Empty / "latest" lets
	// wp-cli fetch the most recent stable. Maps to `wp core download
	// --version=…`. Until 3.0.22 the frontend already sent this field but
	// the backend silently ignored it, so the operator's choice never
	// reached wp-cli.
	Version string `json:"version"`

	// Locale picks the WordPress translation pack (e.g. "en_US", "hi_IN").
	// Maps to `wp core download --locale=…`. Same silent-drop fix as
	// Version above.
	Locale string `json:"locale"`

	// Database setup mode:
	//   "auto"     — panel generates name/user/pass, creates the DB + user
	//                then hands them to WP-CLI. Default, matches the
	//                pre-this-change behavior.
	//   "existing" — operator picked a DB they already created via the
	//                Databases page. Panel does NOT create, just hands
	//                the supplied credentials to WP-CLI.
	//   "manual"   — operator typed in exact DB name / user / pass. Panel
	//                creates them before install. Useful when the
	//                vendor wants a specific name for imports etc.
	// Validation on the backend rather than a struct tag so "" still
	// maps to auto (backward-compat for clients that never sent the
	// field).
	DBMode string `json:"db_mode"`
	DBName string `json:"db_name"`
	DBUser string `json:"db_user"`
	DBPass string `json:"db_pass"`
	DBHost string `json:"db_host"`
}

type WPPlugin struct {
	Name            string `json:"name"`
	Status          string `json:"status"`
	Version         string `json:"version"`
	UpdateAvailable bool   `json:"update_available"`
}

type WPTheme struct {
	Name            string `json:"name"`
	Status          string `json:"status"` // "active" | "inactive" | "parent"
	Version         string `json:"version"`
	UpdateAvailable bool   `json:"update_available"`
}

type WPSecurityScan struct {
	OverallStatus string           `json:"overall_status"`
	Checks        []WPSecurityCheck `json:"checks"`
	ScannedAt     time.Time        `json:"scanned_at"`
}

type WPSecurityCheck struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	Message string   `json:"message"`
	Details []string `json:"details,omitempty"`
}
