package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Database struct {
	ID               primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	DBName           string             `bson:"db_name" json:"db_name"`
	Type             string             `bson:"type" json:"type"` // "mongodb" or "mysql"
	Username         string             `bson:"username" json:"username"`
	Password         string             `bson:"password" json:"-"`
	Domain           string             `bson:"domain" json:"domain"`
	Host             string             `bson:"host" json:"host"`
	Port             int                `bson:"port" json:"port"`
	ConnectionString string            `bson:"connection_string" json:"connection_string"`
	SizeMB           float64           `bson:"size_mb" json:"size_mb"`
	CreatedAt        time.Time         `bson:"created_at" json:"created_at"`
	UpdatedAt        time.Time         `bson:"updated_at" json:"updated_at"`
}

type CreateDatabaseRequest struct {
	DBName   string `json:"db_name" validate:"required"`
	Type     string `json:"type" validate:"required,oneof=mongodb mysql"`
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required,min=8"`
	Domain   string `json:"domain" validate:"required"`
}

type DatabaseUser struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	DatabaseID primitive.ObjectID `bson:"database_id" json:"database_id"`
	Username   string             `bson:"username" json:"username"`
	Password   string             `bson:"password" json:"-"`
	Role       string             `bson:"role" json:"role"`
	CreatedAt  time.Time          `bson:"created_at" json:"created_at"`
}

type CreateDBUserRequest struct {
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required,min=8"`
	Role     string `json:"role" validate:"required,oneof=readWrite read dbAdmin dbOwner userAdmin"`
}

type RemoteAccessRequest struct {
	Username  string `json:"username" validate:"required"`
	AllowedIP string `json:"allowed_ip" validate:"required"`
}
