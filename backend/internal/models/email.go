package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Mailbox struct {
	ID              primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Email           string             `bson:"email" json:"email"`
	Password        string             `bson:"password" json:"-"`
	Domain          string             `bson:"domain" json:"domain"`
	QuotaMB         int                `bson:"quota_mb" json:"quota_mb"`
	UsedMB          float64            `bson:"used_mb" json:"used_mb"`
	SendLimitPerHour int               `bson:"send_limit_per_hour" json:"send_limit_per_hour"`
	CreatedAt       time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt       time.Time          `bson:"updated_at" json:"updated_at"`
}

type CreateMailboxRequest struct {
	Email            string `json:"email" validate:"required,email"`
	Password         string `json:"password" validate:"required,min=8"`
	Domain           string `json:"domain" validate:"required"`
	QuotaMB          int    `json:"quota_mb"`
	SendLimitPerHour int    `json:"send_limit_per_hour"`
}

type EmailForwarder struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Source       string             `bson:"source" json:"source"`
	Destinations []string           `bson:"destinations" json:"destinations"`
	KeepCopy     bool               `bson:"keep_copy" json:"keep_copy"`
	Domain       string             `bson:"domain" json:"domain"`
	CreatedAt    time.Time          `bson:"created_at" json:"created_at"`
}

type Autoresponder struct {
	ID            primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Email         string             `bson:"email" json:"email"`
	Subject       string             `bson:"subject" json:"subject"`
	Body          string             `bson:"body" json:"body"`
	StartDate     *time.Time         `bson:"start_date" json:"start_date"`
	EndDate       *time.Time         `bson:"end_date" json:"end_date"`
	IntervalHours int                `bson:"interval_hours" json:"interval_hours"`
	Domain        string             `bson:"domain" json:"domain"`
	CreatedAt     time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt     time.Time          `bson:"updated_at" json:"updated_at"`
}

type SpamSettings struct {
	Domain        string   `bson:"domain" json:"domain"`
	SpamThreshold float64  `bson:"spam_threshold" json:"spam_threshold"`
	SpamAction    string   `bson:"spam_action" json:"spam_action"`
	Whitelist     []string `bson:"whitelist" json:"whitelist"`
	Blacklist     []string `bson:"blacklist" json:"blacklist"`
	ClamAVEnabled bool     `bson:"clamav_enabled" json:"clamav_enabled"`
}
