package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// TransferComponents specifies which items to migrate.
type TransferComponents struct {
	Hostname     bool `bson:"hostname" json:"hostname"`
	DNS          bool `bson:"dns" json:"dns"`
	SSL          bool `bson:"ssl" json:"ssl"`
	Domains      bool `bson:"domains" json:"domains"`
	Files        bool `bson:"files" json:"files"`
	Databases    bool `bson:"databases" json:"databases"`
	EmailData    bool `bson:"email_data" json:"email_data"`
	FTPAccounts  bool `bson:"ftp_accounts" json:"ftp_accounts"`
	CronJobs     bool `bson:"cron_jobs" json:"cron_jobs"`
	Firewall     bool `bson:"firewall" json:"firewall"`
	ServerConfig bool `bson:"server_config" json:"server_config"`
	Software     bool `bson:"software" json:"software"`
}

// TransferStep tracks progress of a single migration step.
type TransferStep struct {
	Name        string     `bson:"name" json:"name"`
	Status      string     `bson:"status" json:"status"` // pending, in_progress, completed, failed, skipped
	StartedAt   *time.Time `bson:"started_at,omitempty" json:"started_at,omitempty"`
	CompletedAt *time.Time `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	Error       string     `bson:"error,omitempty" json:"error,omitempty"`
	Details     string     `bson:"details,omitempty" json:"details,omitempty"`
}

// TransferLog is a single log entry during a transfer.
type TransferLog struct {
	Timestamp time.Time `bson:"timestamp" json:"timestamp"`
	Level     string    `bson:"level" json:"level"` // info, warn, error
	Message   string    `bson:"message" json:"message"`
	Component string    `bson:"component,omitempty" json:"component,omitempty"`
}

// SourceServer holds connection details for the source server.
type SourceServer struct {
	Hostname string `bson:"hostname" json:"hostname"`
	IP       string `bson:"ip" json:"ip"`
	Port     int    `bson:"port" json:"port"`
	Username string `bson:"username" json:"username"`
	Password string `bson:"password" json:"-"`
	Protocol string `bson:"protocol" json:"protocol"` // ssh
}

// DiscoveredData is what was found on the source server during discovery.
type DiscoveredData struct {
	Hostname     string   `json:"hostname"`
	Domains      []string `json:"domains"`
	Databases    []string `json:"databases"`
	EmailDomains []string `json:"email_domains"`
	CronUsers    []string `json:"cron_users"`
	SSLDomains   []string `json:"ssl_domains"`
	DNSZones     []string `json:"dns_zones"`
	FTPUsers     []string `json:"ftp_users"`
}

// TransferJob is the main transfer/migration record.
type TransferJob struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Type         string             `bson:"type" json:"type"`                   // full, partial
	Direction    string             `bson:"direction" json:"direction"`         // incoming
	SourceServer SourceServer       `bson:"source_server" json:"source_server"`
	Components   TransferComponents `bson:"components" json:"components"`
	Domains      []string           `bson:"domains,omitempty" json:"domains,omitempty"` // specific domains, empty = all
	Status       string             `bson:"status" json:"status"`                       // pending, in_progress, completed, failed, cancelled, partial
	Progress     int                `bson:"progress" json:"progress"`                   // 0-100
	Steps        []TransferStep     `bson:"steps" json:"steps"`
	Logs         []TransferLog      `bson:"logs" json:"logs"`
	Discovered   *DiscoveredData    `bson:"discovered,omitempty" json:"discovered,omitempty"`
	StartedAt    *time.Time         `bson:"started_at,omitempty" json:"started_at,omitempty"`
	CompletedAt  *time.Time         `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	CreatedAt    time.Time          `bson:"created_at" json:"created_at"`
}

// CreateTransferRequest is the request body to start a transfer.
type CreateTransferRequest struct {
	SourceIP   string             `json:"source_ip" validate:"required"`
	SourcePort int                `json:"source_port" validate:"required"`
	Username   string             `json:"username" validate:"required"`
	Password   string             `json:"password" validate:"required"`
	Protocol   string             `json:"protocol" validate:"required,oneof=ssh"`
	Components TransferComponents `json:"components"`
	Domains    []string           `json:"domains"`
}

// DiscoverRequest is the request body to discover what exists on a source server.
type DiscoverRequest struct {
	SourceIP string `json:"source_ip" validate:"required"`
	Port     int    `json:"port" validate:"required"`
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required"`
}
