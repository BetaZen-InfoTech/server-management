package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// SentMessage is the durable record written for every outbound mail. It carries
// the opaque TrackID embedded in the tracking pixel / click-redirect URLs and
// keeps denormalized open/click counters so the "Sent + tracking" list renders
// without scanning the per-event collection.
type SentMessage struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID    primitive.ObjectID `bson:"user_id" json:"user_id"`
	AccountID primitive.ObjectID `bson:"account_id" json:"account_id"`

	TrackID   string `bson:"track_id" json:"track_id"`
	MessageID string `bson:"message_id" json:"message_id"`

	Subject string    `bson:"subject" json:"subject"`
	To      []Address `bson:"to" json:"to"`
	Cc      []Address `bson:"cc,omitempty" json:"cc,omitempty"`
	Bcc     []Address `bson:"bcc,omitempty" json:"bcc,omitempty"`
	Snippet string    `bson:"snippet" json:"snippet"`

	// Which kinds of tracking were active for this message (copied from the
	// mailbox's effective settings at send time).
	TrackDelivery bool `bson:"track_delivery" json:"track_delivery"`
	TrackOpen     bool `bson:"track_open" json:"track_open"`
	TrackClick    bool `bson:"track_click" json:"track_click"`

	// Status: sent → delivered | bounced (delivery feed is best-effort).
	Status      string     `bson:"status" json:"status"`
	OpenCount   int        `bson:"open_count" json:"open_count"`
	ClickCount  int        `bson:"click_count" json:"click_count"`
	FirstOpenAt *time.Time `bson:"first_open_at,omitempty" json:"first_open_at,omitempty"`
	LastOpenAt  *time.Time `bson:"last_open_at,omitempty" json:"last_open_at,omitempty"`

	SentAt time.Time `bson:"sent_at" json:"sent_at"`
}

// TrackingEvent is one recorded interaction (open / click / delivery signal)
// against a SentMessage, keyed by the shared TrackID.
type TrackingEvent struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	TrackID   string             `bson:"track_id" json:"track_id"`
	AccountID primitive.ObjectID `bson:"account_id" json:"account_id"`
	Type      string             `bson:"type" json:"type"` // open | click | delivered | bounced
	URL       string             `bson:"url,omitempty" json:"url,omitempty"`
	IP        string             `bson:"ip,omitempty" json:"ip,omitempty"`
	UserAgent string             `bson:"user_agent,omitempty" json:"user_agent,omitempty"`
	At        time.Time          `bson:"at" json:"at"`
}
