package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Forwarder struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID       primitive.ObjectID `bson:"user_id" json:"user_id"`
	AccountID    primitive.ObjectID `bson:"account_id" json:"account_id"`
	Source       string             `bson:"source" json:"source"`
	Destinations []string           `bson:"destinations" json:"destinations"`
	KeepCopy     bool               `bson:"keep_copy" json:"keep_copy"`
	CreatedAt    time.Time          `bson:"created_at" json:"created_at"`
}

type ForwarderRequest struct {
	AccountID    string   `json:"account_id" validate:"required"`
	Source       string   `json:"source" validate:"required,email"`
	Destinations []string `json:"destinations" validate:"required,min=1,dive,email"`
	KeepCopy     bool     `json:"keep_copy"`
}
