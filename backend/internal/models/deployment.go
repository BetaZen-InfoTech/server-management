package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type GitHubDeploy struct {
	ID              primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Domain          string             `bson:"domain" json:"domain"`
	Repo            string             `bson:"repo" json:"repo"`
	Branch          string             `bson:"branch" json:"branch"`
	AppType         string             `bson:"app_type" json:"app_type"`
	AutoDeploy      bool               `bson:"auto_deploy" json:"auto_deploy"`
	BuildCommand    string             `bson:"build_command" json:"build_command"`
	StartCommand    string             `bson:"start_command" json:"start_command"`
	EnvVars         map[string]string  `bson:"env_vars" json:"env_vars"`
	NodeVersion     string             `bson:"node_version" json:"node_version"`
	RootDir         string             `bson:"root_dir" json:"root_dir"`
	PreDeployScript  string            `bson:"pre_deploy_script" json:"pre_deploy_script"`
	PostDeployScript string            `bson:"post_deploy_script" json:"post_deploy_script"`
	Status          string             `bson:"status" json:"status"`
	CurrentCommit   string             `bson:"current_commit" json:"current_commit"`
	CommitMessage   string             `bson:"commit_message" json:"commit_message"`
	CommitAuthor    string             `bson:"commit_author" json:"commit_author"`
	DeployURL       string             `bson:"deploy_url" json:"deploy_url"`
	WebhookID       string             `bson:"webhook_id" json:"webhook_id"`
	Paused          bool               `bson:"paused" json:"paused"`
	CreatedAt       time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt       time.Time          `bson:"updated_at" json:"updated_at"`
}

type CreateGitHubDeployRequest struct {
	Domain          string            `json:"domain" validate:"required"`
	Repo            string            `json:"repo" validate:"required"`
	Branch          string            `json:"branch" validate:"required"`
	AppType         string            `json:"app_type" validate:"required,oneof=nodejs static php python go docker"`
	AutoDeploy      bool              `json:"auto_deploy"`
	BuildCommand    string            `json:"build_command"`
	StartCommand    string            `json:"start_command"`
	EnvVars         map[string]string `json:"env_vars"`
	NodeVersion     string            `json:"node_version"`
	RootDir         string            `json:"root_dir"`
	PreDeployScript  string           `json:"pre_deploy_script"`
	PostDeployScript string           `json:"post_deploy_script"`
}

type DeployRelease struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	DeployID       primitive.ObjectID `bson:"deploy_id" json:"deploy_id"`
	Commit         string             `bson:"commit" json:"commit"`
	CommitMessage  string             `bson:"commit_message" json:"commit_message"`
	Author         string             `bson:"author" json:"author"`
	Branch         string             `bson:"branch" json:"branch"`
	Status         string             `bson:"status" json:"status"`
	Trigger        string             `bson:"trigger" json:"trigger"`
	DurationSeconds int               `bson:"duration_seconds" json:"duration_seconds"`
	Logs           []string           `bson:"logs" json:"logs"`
	DeployedAt     time.Time          `bson:"deployed_at" json:"deployed_at"`
}

type DeployAPIKey struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Key          string             `bson:"key" json:"key"`
	Name         string             `bson:"name" json:"name"`
	DeploymentID primitive.ObjectID `bson:"deployment_id" json:"deployment_id"`
	CreatedAt    time.Time          `bson:"created_at" json:"created_at"`
}

type GitHubConnection struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID         primitive.ObjectID `bson:"user_id" json:"user_id"`
	GitHubUsername string             `bson:"github_username" json:"github_username"`
	AccessToken    string             `bson:"access_token" json:"-"`
	Method         string             `bson:"method" json:"method"`
	Scopes         []string           `bson:"scopes" json:"scopes"`
	ConnectedAt    time.Time          `bson:"connected_at" json:"connected_at"`
}
