package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Campaign statuses.
const (
	CampaignDraft    = "draft"
	CampaignSending  = "sending"
	CampaignPaused   = "paused"
	CampaignSent     = "sent"
	CampaignCanceled = "canceled"
	CampaignFailed   = "failed"

	CampaignModeNow  = "now"  // send as fast as batches allow
	CampaignModeDrip = "drip" // BatchSize recipients every IntervalSeconds
)

// Campaign is a marketing email sent to the subscribed contacts of one or more
// groups, from a chosen mailbox. It's driven by the background worker: recipients
// are materialized once (one CampaignRecipient per contact) then sent in batches.
type Campaign struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID      primitive.ObjectID `bson:"user_id" json:"user_id"`
	AccountID   primitive.ObjectID `bson:"account_id" json:"account_id"`
	Name        string             `bson:"name" json:"name"`
	Subject     string             `bson:"subject" json:"subject"`
	HTML        string             `bson:"html" json:"html"`
	SignatureID primitive.ObjectID `bson:"signature_id,omitempty" json:"signature_id,omitempty"`

	// Targeting: subscribed contacts that belong to any of these groups.
	GroupIDs []primitive.ObjectID `bson:"group_ids" json:"group_ids"`

	// Sending policy.
	Mode            string `bson:"mode" json:"mode"`
	BatchSize       int    `bson:"batch_size" json:"batch_size"`
	IntervalSeconds int    `bson:"interval_seconds" json:"interval_seconds"`

	// State.
	Status          string     `bson:"status" json:"status"`
	TotalRecipients int        `bson:"total_recipients" json:"total_recipients"`
	SentCount       int        `bson:"sent_count" json:"sent_count"`
	FailedCount     int        `bson:"failed_count" json:"failed_count"`
	NextRunAt       *time.Time `bson:"next_run_at,omitempty" json:"next_run_at,omitempty"`
	StartedAt       *time.Time `bson:"started_at,omitempty" json:"started_at,omitempty"`
	CompletedAt     *time.Time `bson:"completed_at,omitempty" json:"completed_at,omitempty"`

	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// CampaignRecipient is one contact in one campaign — the unit of per-recipient
// tracking. Each carries its own TrackID so opens/clicks are attributed to the
// individual recipient (unlike a normal send, which is per-message).
type CampaignRecipient struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	CampaignID primitive.ObjectID `bson:"campaign_id" json:"campaign_id"`
	UserID     primitive.ObjectID `bson:"user_id" json:"user_id"`
	AccountID  primitive.ObjectID `bson:"account_id" json:"account_id"`
	ContactID  primitive.ObjectID `bson:"contact_id" json:"contact_id"`
	Email      string             `bson:"email" json:"email"`
	Name       string             `bson:"name" json:"name"`
	Fields     map[string]string  `bson:"fields,omitempty" json:"fields,omitempty"`

	TrackID    string `bson:"track_id" json:"track_id"`
	MessageID  string `bson:"message_id,omitempty" json:"message_id,omitempty"`
	UnsubToken string `bson:"unsub_token" json:"-"`

	Status      string     `bson:"status" json:"status"` // pending|sent|failed|bounced|unsubscribed
	OpenCount   int        `bson:"open_count" json:"open_count"`
	ClickCount  int        `bson:"click_count" json:"click_count"`
	FirstOpenAt *time.Time `bson:"first_open_at,omitempty" json:"first_open_at,omitempty"`
	LastOpenAt  *time.Time `bson:"last_open_at,omitempty" json:"last_open_at,omitempty"`
	SentAt      *time.Time `bson:"sent_at,omitempty" json:"sent_at,omitempty"`
	Error       string     `bson:"error,omitempty" json:"error,omitempty"`

	CreatedAt time.Time `bson:"created_at" json:"created_at"`
}

// Recipient statuses.
const (
	RecipientPending      = "pending"
	RecipientSent         = "sent"
	RecipientFailed       = "failed"
	RecipientBounced      = "bounced"
	RecipientUnsubscribed = "unsubscribed"
)

// CampaignRequest is the create/update body.
type CampaignRequest struct {
	AccountID       string   `json:"account_id" validate:"required"`
	Name            string   `json:"name" validate:"required"`
	Subject         string   `json:"subject" validate:"required"`
	HTML            string   `json:"html" validate:"required"`
	SignatureID     string   `json:"signature_id"`
	GroupIDs        []string `json:"group_ids" validate:"required,min=1"`
	Mode            string   `json:"mode"`
	BatchSize       int      `json:"batch_size"`
	IntervalSeconds int      `json:"interval_seconds"`
}

// CampaignStats is the live analytics rollup computed from recipients.
type CampaignStats struct {
	Total        int `json:"total"`
	Pending      int `json:"pending"`
	Sent         int `json:"sent"`
	Failed       int `json:"failed"`
	Bounced      int `json:"bounced"`
	Unsubscribed int `json:"unsubscribed"`
	Opened       int `json:"opened"` // recipients with >=1 open
	Clicked      int `json:"clicked"`
	OpenTotal    int `json:"open_total"`
	ClickTotal   int `json:"click_total"`
}
