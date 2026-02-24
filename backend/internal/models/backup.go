package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Backup struct {
	ID                primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Type              string             `bson:"type" json:"type"`
	Domain            string             `bson:"domain" json:"domain"`
	User              string             `bson:"user" json:"user"`
	Storage           string             `bson:"storage" json:"storage"`
	Status            string             `bson:"status" json:"status"`
	SizeMB            float64            `bson:"size_mb" json:"size_mb"`
	FileCount         int                `bson:"file_count" json:"file_count"`
	DatabasesIncluded []string           `bson:"databases_included" json:"databases_included"`
	Path              string             `bson:"path" json:"path"`
	Encrypted         bool               `bson:"encrypted" json:"encrypted"`
	Compression       string             `bson:"compression" json:"compression"`
	S3Bucket          string             `bson:"s3_bucket" json:"s3_bucket,omitempty"`
	S3Region          string             `bson:"s3_region" json:"s3_region,omitempty"`
	CreatedAt         time.Time          `bson:"created_at" json:"created_at"`
	CompletedAt       *time.Time         `bson:"completed_at" json:"completed_at"`
}

type CreateBackupRequest struct {
	Type               string `json:"type" validate:"required,oneof=full files database email config"`
	User               string `json:"user" validate:"required"`
	Domain             string `json:"domain" validate:"required"`
	Storage            string `json:"storage" validate:"required,oneof=local s3"`
	Compression        string `json:"compression" validate:"omitempty,oneof=gzip zstd"`
	EncryptionPassword string `json:"encryption_password"`
	S3Bucket           string `json:"s3_bucket"`
	S3Region           string `json:"s3_region"`
	S3AccessKey        string `json:"s3_access_key"`
	S3SecretKey        string `json:"s3_secret_key"`
	S3Endpoint         string `json:"s3_endpoint"`
}

type RestoreRequest struct {
	BackupID           string `json:"backup_id" validate:"required"`
	RestoreType        string `json:"restore_type" validate:"required,oneof=full files database email config"`
	Overwrite          bool   `json:"overwrite"`
	EncryptionPassword string `json:"encryption_password"`
}

type BackupSchedule struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Domain         string             `bson:"domain" json:"domain"`
	User           string             `bson:"user" json:"user"`
	Type           string             `bson:"type" json:"type"`
	Storage        string             `bson:"storage" json:"storage"`
	Schedule       string             `bson:"schedule" json:"schedule"`
	Time           string             `bson:"time" json:"time"`
	Timezone       string             `bson:"timezone" json:"timezone"`
	RetentionCount int                `bson:"retention_count" json:"retention_count"`
	S3Bucket       string             `bson:"s3_bucket" json:"s3_bucket"`
	S3Region       string             `bson:"s3_region" json:"s3_region"`
	S3AccessKey    string             `bson:"s3_access_key" json:"-"`
	S3SecretKey    string             `bson:"s3_secret_key" json:"-"`
	NotifyEmail    string             `bson:"notify_email" json:"notify_email"`
	Enabled        bool               `bson:"enabled" json:"enabled"`
	CreatedAt      time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt      time.Time          `bson:"updated_at" json:"updated_at"`
}
