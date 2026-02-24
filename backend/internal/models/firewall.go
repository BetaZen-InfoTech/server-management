package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type FirewallRule struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Port      string             `bson:"port" json:"port"`
	Protocol  string             `bson:"protocol" json:"protocol"`
	Action    string             `bson:"action" json:"action"`
	Source    string             `bson:"source" json:"source"`
	Comment   string             `bson:"comment" json:"comment"`
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
}

type AllowPortRequest struct {
	Port     string `json:"port" validate:"required"`
	Protocol string `json:"protocol" validate:"required,oneof=tcp udp"`
	Source   string `json:"source"`
	Comment  string `json:"comment"`
}

type BlockedIP struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	IP        string             `bson:"ip" json:"ip"`
	Reason    string             `bson:"reason" json:"reason"`
	Duration  string             `bson:"duration" json:"duration"`
	ExpiresAt *time.Time         `bson:"expires_at" json:"expires_at"`
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
}

type BlockIPRequest struct {
	IP       string `json:"ip" validate:"required"`
	Reason   string `json:"reason"`
	Duration string `json:"duration" validate:"required,oneof=permanent 1h 6h 24h 7d 30d"`
}

type Fail2BanConfig struct {
	Jail       string `json:"jail" validate:"required"`
	BanTime    int    `json:"ban_time"`
	MaxRetries int    `json:"max_retries"`
	FindTime   int    `json:"find_time"`
}

type FirewallStatus struct {
	Enabled           bool   `json:"enabled"`
	DefaultIncoming   string `json:"default_incoming"`
	DefaultOutgoing   string `json:"default_outgoing"`
	RulesCount        int    `json:"rules_count"`
	BlockedIPsCount   int    `json:"blocked_ips_count"`
	Fail2BanActive    bool   `json:"fail2ban_active"`
	Fail2BanTotalBans int    `json:"fail2ban_total_bans"`
}
