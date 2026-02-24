package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type AuditLog struct {
	ID           primitive.ObjectID     `bson:"_id,omitempty" json:"id"`
	Timestamp    time.Time              `bson:"timestamp" json:"timestamp"`
	User         AuditUser              `bson:"user" json:"user"`
	Action       string                 `bson:"action" json:"action"`
	ResourceType string                 `bson:"resource_type" json:"resource_type"`
	ResourceID   string                 `bson:"resource_id" json:"resource_id"`
	Description  string                 `bson:"description" json:"description"`
	IPAddress    string                 `bson:"ip_address" json:"ip_address"`
	UserAgent    string                 `bson:"user_agent" json:"user_agent"`
	Status       string                 `bson:"status" json:"status"`
	Metadata     map[string]interface{} `bson:"metadata" json:"metadata"`
}

type AuditUser struct {
	ID    string `bson:"id" json:"id"`
	Email string `bson:"email" json:"email"`
	Role  string `bson:"role" json:"role"`
}
