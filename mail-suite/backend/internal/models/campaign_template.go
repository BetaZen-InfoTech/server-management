package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// CampaignTemplate is a reusable campaign body (subject + HTML) a user can save
// once and start new campaigns from.
type CampaignTemplate struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID    primitive.ObjectID `bson:"user_id" json:"user_id"`
	Name      string             `bson:"name" json:"name"`
	Subject   string             `bson:"subject" json:"subject"`
	HTML      string             `bson:"html" json:"html"`
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time          `bson:"updated_at" json:"updated_at"`
}

type CampaignTemplateRequest struct {
	Name    string `json:"name" validate:"required"`
	Subject string `json:"subject"`
	HTML    string `json:"html" validate:"required"`
}
