package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type NotificationSettings struct {
	ID    primitive.ObjectID   `bson:"_id,omitempty" json:"id"`
	Email NotificationChannel  `bson:"email" json:"email"`
	Slack NotificationChannel  `bson:"slack" json:"slack"`
}

type NotificationChannel struct {
	Enabled    bool     `bson:"enabled" json:"enabled"`
	Recipients []string `bson:"recipients,omitempty" json:"recipients,omitempty"`
	WebhookURL string   `bson:"webhook_url,omitempty" json:"webhook_url,omitempty"`
	Channel    string   `bson:"channel,omitempty" json:"channel,omitempty"`
	Events     []string `bson:"events" json:"events"`
}

type Webhook struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	URL       string             `bson:"url" json:"url"`
	Secret    string             `bson:"secret" json:"-"`
	Events    []string           `bson:"events" json:"events"`
	Active    bool               `bson:"active" json:"active"`
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time          `bson:"updated_at" json:"updated_at"`
}

type CreateWebhookRequest struct {
	URL    string   `json:"url" validate:"required,url"`
	Secret string   `json:"secret" validate:"required"`
	Events []string `json:"events" validate:"required,min=1"`
	Active bool     `json:"active"`
}

type NotificationHistory struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Event     string             `bson:"event" json:"event"`
	Channel   string             `bson:"channel" json:"channel"`
	Recipient string             `bson:"recipient" json:"recipient"`
	Status    string             `bson:"status" json:"status"`
	Message   string             `bson:"message" json:"message"`
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
}
