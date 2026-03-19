package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type FTPAccount struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Username  string             `bson:"username" json:"username"`
	Domain    string             `bson:"domain" json:"domain"`
	HomeDir   string             `bson:"home_dir" json:"home_dir"`
	IsRoot    bool               `bson:"is_root" json:"is_root"` // root FTP accounts cannot be deleted
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time          `bson:"updated_at" json:"updated_at"`
}
