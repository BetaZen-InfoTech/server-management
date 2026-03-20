package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// HostingPackage defines a reusable resource plan that can be assigned to customer accounts.
type HostingPackage struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Name      string             `bson:"name" json:"name"`
	CreatedBy string             `bson:"created_by" json:"created_by"`

	// Resources
	DiskQuotaMB        int  `bson:"disk_quota_mb" json:"disk_quota_mb"`
	DiskQuotaUnlimited bool `bson:"disk_quota_unlimited" json:"disk_quota_unlimited"`
	BandwidthMB        int  `bson:"bandwidth_mb" json:"bandwidth_mb"`
	BandwidthUnlimited bool `bson:"bandwidth_unlimited" json:"bandwidth_unlimited"`
	MaxFTPAccounts        int  `bson:"max_ftp_accounts" json:"max_ftp_accounts"`
	MaxFTPUnlimited       bool `bson:"max_ftp_unlimited" json:"max_ftp_unlimited"`
	MaxEmailAccounts      int  `bson:"max_email_accounts" json:"max_email_accounts"`
	MaxEmailUnlimited     bool `bson:"max_email_unlimited" json:"max_email_unlimited"`
	MaxMailingLists       int  `bson:"max_mailing_lists" json:"max_mailing_lists"`
	MaxMailingUnlimited   bool `bson:"max_mailing_unlimited" json:"max_mailing_unlimited"`
	MaxDatabases          int  `bson:"max_databases" json:"max_databases"`
	MaxDatabasesUnlimited bool `bson:"max_databases_unlimited" json:"max_databases_unlimited"`
	MaxSubDomains         int  `bson:"max_subdomains" json:"max_subdomains"`
	MaxSubDomainsUnlimited bool `bson:"max_subdomains_unlimited" json:"max_subdomains_unlimited"`
	MaxParkedDomains      int  `bson:"max_parked_domains" json:"max_parked_domains"`
	MaxParkedUnlimited    bool `bson:"max_parked_unlimited" json:"max_parked_unlimited"`
	MaxAddonDomains       int  `bson:"max_addon_domains" json:"max_addon_domains"`
	MaxAddonUnlimited     bool `bson:"max_addon_unlimited" json:"max_addon_unlimited"`
	MaxPassengerApps      int  `bson:"max_passenger_apps" json:"max_passenger_apps"`
	MaxPassengerUnlimited bool `bson:"max_passenger_unlimited" json:"max_passenger_unlimited"`
	MaxHourlyEmail        int  `bson:"max_hourly_email" json:"max_hourly_email"`
	MaxHourlyEmailUnlimited bool `bson:"max_hourly_email_unlimited" json:"max_hourly_email_unlimited"`
	MaxFailPercent        int  `bson:"max_fail_percent" json:"max_fail_percent"`
	MaxEmailQuotaMB       int  `bson:"max_email_quota_mb" json:"max_email_quota_mb"`
	MaxEmailQuotaUnlimited bool `bson:"max_email_quota_unlimited" json:"max_email_quota_unlimited"`

	// Settings
	DedicatedIP    bool   `bson:"dedicated_ip" json:"dedicated_ip"`
	ShellAccess    bool   `bson:"shell_access" json:"shell_access"`
	CGIAccess      bool   `bson:"cgi_access" json:"cgi_access"`
	DigestAuth     bool   `bson:"digest_auth" json:"digest_auth"`
	Theme          string `bson:"theme" json:"theme"`
	FeatureList    string `bson:"feature_list" json:"feature_list"`
	Locale         string `bson:"locale" json:"locale"`

	// Extensions
	WPToolkit   bool `bson:"wp_toolkit" json:"wp_toolkit"`
	LVEEnabled  bool `bson:"lve_enabled" json:"lve_enabled"`

	// LVE resource limits
	LVESpeed     int    `bson:"lve_speed" json:"lve_speed"`
	LVESpeedMySQL int   `bson:"lve_speed_mysql" json:"lve_speed_mysql"`
	LVEVMEM      int    `bson:"lve_vmem" json:"lve_vmem"`
	LVEPMEM      int    `bson:"lve_pmem" json:"lve_pmem"`
	LVEIO        int    `bson:"lve_io" json:"lve_io"`
	LVEMySQLIO   string `bson:"lve_mysql_io" json:"lve_mysql_io"`
	LVEIOPS      int    `bson:"lve_iops" json:"lve_iops"`
	LVEEP        int    `bson:"lve_ep" json:"lve_ep"`
	LVENPROC     int    `bson:"lve_nproc" json:"lve_nproc"`
	LVEINODESSoft int   `bson:"lve_inodes_soft" json:"lve_inodes_soft"`
	LVEINODESHard int   `bson:"lve_inodes_hard" json:"lve_inodes_hard"`

	AccountCount int       `bson:"account_count" json:"account_count"`
	CreatedAt    time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt    time.Time `bson:"updated_at" json:"updated_at"`
}

type CreatePackageRequest struct {
	Name string `json:"name" validate:"required"`

	// Resources
	DiskQuotaMB        int  `json:"disk_quota_mb"`
	DiskQuotaUnlimited bool `json:"disk_quota_unlimited"`
	BandwidthMB        int  `json:"bandwidth_mb"`
	BandwidthUnlimited bool `json:"bandwidth_unlimited"`
	MaxFTPAccounts        int  `json:"max_ftp_accounts"`
	MaxFTPUnlimited       bool `json:"max_ftp_unlimited"`
	MaxEmailAccounts      int  `json:"max_email_accounts"`
	MaxEmailUnlimited     bool `json:"max_email_unlimited"`
	MaxMailingLists       int  `json:"max_mailing_lists"`
	MaxMailingUnlimited   bool `json:"max_mailing_unlimited"`
	MaxDatabases          int  `json:"max_databases"`
	MaxDatabasesUnlimited bool `json:"max_databases_unlimited"`
	MaxSubDomains         int  `json:"max_subdomains"`
	MaxSubDomainsUnlimited bool `json:"max_subdomains_unlimited"`
	MaxParkedDomains      int  `json:"max_parked_domains"`
	MaxParkedUnlimited    bool `json:"max_parked_unlimited"`
	MaxAddonDomains       int  `json:"max_addon_domains"`
	MaxAddonUnlimited     bool `json:"max_addon_unlimited"`
	MaxPassengerApps      int  `json:"max_passenger_apps"`
	MaxPassengerUnlimited bool `json:"max_passenger_unlimited"`
	MaxHourlyEmail        int  `json:"max_hourly_email"`
	MaxHourlyEmailUnlimited bool `json:"max_hourly_email_unlimited"`
	MaxFailPercent        int  `json:"max_fail_percent"`
	MaxEmailQuotaMB       int  `json:"max_email_quota_mb"`
	MaxEmailQuotaUnlimited bool `json:"max_email_quota_unlimited"`

	// Settings
	DedicatedIP    bool   `json:"dedicated_ip"`
	ShellAccess    bool   `json:"shell_access"`
	CGIAccess      bool   `json:"cgi_access"`
	DigestAuth     bool   `json:"digest_auth"`
	Theme          string `json:"theme"`
	FeatureList    string `json:"feature_list"`
	Locale         string `json:"locale"`

	// Extensions
	WPToolkit   bool `json:"wp_toolkit"`
	LVEEnabled  bool `json:"lve_enabled"`

	// LVE limits
	LVESpeed      int    `json:"lve_speed"`
	LVESpeedMySQL int    `json:"lve_speed_mysql"`
	LVEVMEM       int    `json:"lve_vmem"`
	LVEPMEM       int    `json:"lve_pmem"`
	LVEIO         int    `json:"lve_io"`
	LVEMySQLIO    string `json:"lve_mysql_io"`
	LVEIOPS       int    `json:"lve_iops"`
	LVEEP         int    `json:"lve_ep"`
	LVENPROC      int    `json:"lve_nproc"`
	LVEINODESSoft int    `json:"lve_inodes_soft"`
	LVEINODESHard int    `json:"lve_inodes_hard"`
}
