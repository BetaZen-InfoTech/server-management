package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// ContactGroup is a named list/segment of contacts a campaign can target. A
// contact can be in many groups; membership lives on Contact.GroupIDs.
// ContactCount is denormalized for fast listing and recomputed on membership
// changes.
type ContactGroup struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID       primitive.ObjectID `bson:"user_id" json:"user_id"`
	Name         string             `bson:"name" json:"name"`
	Description  string             `bson:"description,omitempty" json:"description,omitempty"`
	Color        string             `bson:"color,omitempty" json:"color,omitempty"`
	ContactCount int                `bson:"contact_count" json:"contact_count"`
	CreatedAt    time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt    time.Time          `bson:"updated_at" json:"updated_at"`
}

type ContactGroupRequest struct {
	Name        string `json:"name" validate:"required"`
	Description string `json:"description"`
	Color       string `json:"color"`
}
