package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type App struct {
	ID              primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Name            string             `bson:"name" json:"name"`
	Domain          string             `bson:"domain" json:"domain"`
	AppType         string             `bson:"app_type" json:"app_type"`
	DeployMethod    string             `bson:"deploy_method" json:"deploy_method"`
	User            string             `bson:"user" json:"user"`
	Port            int                `bson:"port" json:"port"`
	GitURL          string             `bson:"git_url" json:"git_url"`
	GitBranch       string             `bson:"git_branch" json:"git_branch"`
	GitToken        string             `bson:"git_token" json:"-"`
	DockerImage     string             `bson:"docker_image" json:"docker_image"`
	DockerVolumes   []string           `bson:"docker_volumes" json:"docker_volumes"`
	DockerNetwork   string             `bson:"docker_network" json:"docker_network"`
	BuildCmd        string             `bson:"build_cmd" json:"build_cmd"`
	StartCmd        string             `bson:"start_cmd" json:"start_cmd"`
	HealthCheckPath string             `bson:"health_check_path" json:"health_check_path"`
	MinInstances    int                `bson:"min_instances" json:"min_instances"`
	MaxInstances    int                `bson:"max_instances" json:"max_instances"`
	EnvVars         map[string]string  `bson:"env_vars" json:"env_vars"`
	Status          string             `bson:"status" json:"status"`
	PID             int                `bson:"pid" json:"pid"`
	MemoryMB        float64            `bson:"memory_mb" json:"memory_mb"`
	CPUPercent      float64            `bson:"cpu_percent" json:"cpu_percent"`
	Uptime          string             `bson:"uptime" json:"uptime"`
	DeploymentsCount int               `bson:"deployments_count" json:"deployments_count"`
	LastDeployed    *time.Time         `bson:"last_deployed" json:"last_deployed"`
	CreatedAt       time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt       time.Time          `bson:"updated_at" json:"updated_at"`
}

type DeployAppRequest struct {
	Name            string            `json:"name" validate:"required"`
	Domain          string            `json:"domain" validate:"required"`
	AppType         string            `json:"app_type" validate:"required,oneof=go node python ruby rust java static docker"`
	DeployMethod    string            `json:"deploy_method" validate:"required,oneof=git zip binary docker"`
	User            string            `json:"user" validate:"required"`
	Port            int               `json:"port" validate:"required"`
	GitURL          string            `json:"git_url"`
	GitBranch       string            `json:"git_branch"`
	GitToken        string            `json:"git_token"`
	DockerImage     string            `json:"docker_image"`
	DockerVolumes   []string          `json:"docker_volumes"`
	DockerNetwork   string            `json:"docker_network"`
	BuildCmd        string            `json:"build_cmd"`
	StartCmd        string            `json:"start_cmd"`
	HealthCheckPath string            `json:"health_check_path"`
	MinInstances    int               `json:"min_instances"`
	MaxInstances    int               `json:"max_instances"`
	EnvVars         map[string]string `json:"env_vars"`
}

type AppDeployment struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	AppName     string             `bson:"app_name" json:"app_name"`
	Version     int                `bson:"version" json:"version"`
	GitCommit   string             `bson:"git_commit" json:"git_commit"`
	Status      string             `bson:"status" json:"status"`
	BackupPath  string             `bson:"backup_path" json:"backup_path"`
	CreatedAt   time.Time          `bson:"created_at" json:"created_at"`
}
