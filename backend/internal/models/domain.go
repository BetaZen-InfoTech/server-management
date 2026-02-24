package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Domain struct {
	ID               primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Domain           string             `bson:"domain" json:"domain"`
	User             string             `bson:"user" json:"user"`
	Password         string             `bson:"password" json:"-"`
	PHPVersion       string             `bson:"php_version" json:"php_version"`
	DiskQuotaMB      int                `bson:"disk_quota_mb" json:"disk_quota_mb"`
	BandwidthLimitGB int                `bson:"bandwidth_limit_gb" json:"bandwidth_limit_gb"`
	MaxDatabases     int                `bson:"max_databases" json:"max_databases"`
	MaxEmailAccounts int                `bson:"max_email_accounts" json:"max_email_accounts"`
	MaxSubdomains    int                `bson:"max_subdomains" json:"max_subdomains"`
	MaxApps          int                `bson:"max_apps" json:"max_apps"`
	SSLActive        bool               `bson:"ssl_active" json:"ssl_active"`
	SSLExpires       *time.Time         `bson:"ssl_expires" json:"ssl_expires"`
	Status           string             `bson:"status" json:"status"`
	CreatedAt        time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt        time.Time          `bson:"updated_at" json:"updated_at"`
}

type CreateDomainRequest struct {
	Domain           string `json:"domain" validate:"required"`
	User             string `json:"user" validate:"required"`
	Password         string `json:"password" validate:"required,min=8"`
	PHPVersion       string `json:"php_version" validate:"required,oneof=7.4 8.0 8.1 8.2 8.3"`
	DiskQuotaMB      int    `json:"disk_quota_mb"`
	BandwidthLimitGB int    `json:"bandwidth_limit_gb"`
	MaxDatabases     int    `json:"max_databases"`
	MaxEmailAccounts int    `json:"max_email_accounts"`
	MaxSubdomains    int    `json:"max_subdomains"`
	MaxApps          int    `json:"max_apps"`
}

type Subdomain struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	DomainID     primitive.ObjectID `bson:"domain_id" json:"domain_id"`
	Subdomain    string             `bson:"subdomain" json:"subdomain"`
	DocumentRoot string             `bson:"document_root" json:"document_root"`
	PHPVersion   string             `bson:"php_version" json:"php_version"`
	CreatedAt    time.Time          `bson:"created_at" json:"created_at"`
}

type DomainAlias struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	DomainID    primitive.ObjectID `bson:"domain_id" json:"domain_id"`
	AliasDomain string             `bson:"alias_domain" json:"alias_domain"`
	CreatedAt   time.Time          `bson:"created_at" json:"created_at"`
}

type DomainRedirect struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	DomainID   primitive.ObjectID `bson:"domain_id" json:"domain_id"`
	SourcePath string             `bson:"source_path" json:"source_path"`
	TargetURL  string             `bson:"target_url" json:"target_url"`
	Type       string             `bson:"type" json:"type"`
	MatchType  string             `bson:"match_type" json:"match_type"`
	CreatedAt  time.Time          `bson:"created_at" json:"created_at"`
}
