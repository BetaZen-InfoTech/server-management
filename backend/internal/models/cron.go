package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type CronJob struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Domain      string             `bson:"domain" json:"domain"`
	User        string             `bson:"user" json:"user"`
	Command     string             `bson:"command" json:"command"`
	Schedule    string             `bson:"schedule" json:"schedule"`
	Description string             `bson:"description" json:"description"`
	NotifyEmail string             `bson:"notify_email" json:"notify_email"`
	NotifyOn    string             `bson:"notify_on" json:"notify_on"`
	Enabled     bool               `bson:"enabled" json:"enabled"`
	CreatedAt   time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time          `bson:"updated_at" json:"updated_at"`
}

type CreateCronRequest struct {
	Domain      string `json:"domain" validate:"required"`
	User        string `json:"user" validate:"required"`
	Command     string `json:"command" validate:"required"`
	Schedule    string `json:"schedule" validate:"required"`
	Description string `json:"description"`
	NotifyEmail string `json:"notify_email" validate:"omitempty,email"`
	NotifyOn    string `json:"notify_on" validate:"omitempty,oneof=never always failure success"`
	Enabled     bool   `json:"enabled"`
}

type CronExecution struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	CronJobID  primitive.ObjectID `bson:"cron_job_id" json:"cron_job_id"`
	ExecutedAt time.Time          `bson:"executed_at" json:"executed_at"`
	ExitCode   int                `bson:"exit_code" json:"exit_code"`
	DurationMS int64              `bson:"duration_ms" json:"duration_ms"`
	Output     string             `bson:"output" json:"output"`
	Status     string             `bson:"status" json:"status"`
}
