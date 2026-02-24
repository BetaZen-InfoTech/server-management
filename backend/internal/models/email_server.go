package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// EmailServerConfig stores the desired email server component configuration (singleton per server).
type EmailServerConfig struct {
	ID                 primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Hostname           string             `bson:"hostname" json:"hostname"`
	Domain             string             `bson:"domain" json:"domain"`
	PostfixEnabled     bool               `bson:"postfix_enabled" json:"postfix_enabled"`
	DovecotEnabled     bool               `bson:"dovecot_enabled" json:"dovecot_enabled"`
	SpamAssassinEnabled bool              `bson:"spamassassin_enabled" json:"spamassassin_enabled"`
	OpenDKIMEnabled    bool               `bson:"opendkim_enabled" json:"opendkim_enabled"`
	ClamAVEnabled      bool               `bson:"clamav_enabled" json:"clamav_enabled"`
	Status             string             `bson:"status" json:"status"` // not_installed, installing, installed, failed
	InstalledAt        *time.Time         `bson:"installed_at,omitempty" json:"installed_at,omitempty"`
	CreatedAt          time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt          time.Time          `bson:"updated_at" json:"updated_at"`
}

// EmailInstallStep represents a single step in the installation process.
type EmailInstallStep struct {
	Name        string     `bson:"name" json:"name"`
	Description string     `bson:"description" json:"description"`
	Status      string     `bson:"status" json:"status"` // pending, running, completed, failed, skipped
	Output      string     `bson:"output" json:"output"`
	Error       string     `bson:"error,omitempty" json:"error,omitempty"`
	StartedAt   *time.Time `bson:"started_at,omitempty" json:"started_at,omitempty"`
	CompletedAt *time.Time `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
}

// EmailInstallation tracks a full installation job with all its steps.
type EmailInstallation struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	ConfigID     primitive.ObjectID `bson:"config_id" json:"config_id"`
	Status       string             `bson:"status" json:"status"` // pending, running, completed, failed
	Steps        []EmailInstallStep `bson:"steps" json:"steps"`
	CurrentStep  int                `bson:"current_step" json:"current_step"`
	TotalSteps   int                `bson:"total_steps" json:"total_steps"`
	StartedAt    *time.Time         `bson:"started_at,omitempty" json:"started_at,omitempty"`
	CompletedAt  *time.Time         `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	ErrorMessage string             `bson:"error_message,omitempty" json:"error_message,omitempty"`
	CreatedAt    time.Time          `bson:"created_at" json:"created_at"`
}

// EmailComponentStatus represents the runtime status of a single email component.
type EmailComponentStatus struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Enabled   bool   `json:"enabled"`
	Version   string `json:"version"`
}

// InstallEmailRequest is the request body for starting email server installation.
type InstallEmailRequest struct {
	Hostname           string `json:"hostname" validate:"required"`
	Domain             string `json:"domain" validate:"required"`
	SpamAssassinEnabled bool  `json:"spamassassin_enabled"`
	OpenDKIMEnabled    bool   `json:"opendkim_enabled"`
	ClamAVEnabled      bool   `json:"clamav_enabled"`
}

// UpdateEmailSettingsRequest is the request body for updating email server settings.
type UpdateEmailSettingsRequest struct {
	Hostname           string `json:"hostname"`
	Domain             string `json:"domain"`
	SpamAssassinEnabled *bool `json:"spamassassin_enabled"`
	OpenDKIMEnabled    *bool  `json:"opendkim_enabled"`
	ClamAVEnabled      *bool  `json:"clamav_enabled"`
}
