package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Device struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID    primitive.ObjectID `bson:"user_id" json:"user_id"`
	Platform  string             `bson:"platform" json:"platform"`
	Model     string             `bson:"model,omitempty" json:"model,omitempty"`
	FCMToken  string             `bson:"fcm_token" json:"fcm_token"`
	AppVer    string             `bson:"app_ver,omitempty" json:"app_ver,omitempty"`
	LastSeen  time.Time          `bson:"last_seen" json:"last_seen"`
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
}

type RegisterDeviceRequest struct {
	Platform string `json:"platform" validate:"required,oneof=android ios web"`
	Model    string `json:"model"`
	FCMToken string `json:"fcm_token" validate:"required"`
	AppVer   string `json:"app_ver"`
}
