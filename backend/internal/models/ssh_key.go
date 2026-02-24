package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type SSHKey struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	User        string             `bson:"user" json:"user"`
	Name        string             `bson:"name" json:"name"`
	PublicKey   string             `bson:"public_key" json:"public_key"`
	KeyType     string             `bson:"key_type" json:"key_type"`
	Fingerprint string             `bson:"fingerprint" json:"fingerprint"`
	CreatedAt   time.Time          `bson:"created_at" json:"created_at"`
}

type AddSSHKeyRequest struct {
	Name      string `json:"name" validate:"required"`
	PublicKey string `json:"public_key" validate:"required"`
	KeyType   string `json:"key_type" validate:"required,oneof=login deploy"`
}
