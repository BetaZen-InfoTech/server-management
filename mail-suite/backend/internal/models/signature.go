package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Signature struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID    primitive.ObjectID `bson:"user_id" json:"user_id"`
	Name      string             `bson:"name" json:"name"`
	HTML      string             `bson:"html" json:"html"`
	IsDefault bool               `bson:"is_default" json:"is_default"`
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time          `bson:"updated_at" json:"updated_at"`
}

type SignatureRequest struct {
	Name      string `json:"name" validate:"required"`
	HTML      string `json:"html" validate:"required"`
	IsDefault bool   `json:"is_default"`
}
