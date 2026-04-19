package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Project is a "Deploy Software" entry — a logical software product composed of
// one or more cooperating services (backend APIs, frontend SPAs, static sites).
//
// The PAT is stored AES-GCM encrypted at rest in GitHubPATEncrypted; the JSON
// response never echoes it back. GitHubPATMasked holds a "ghp_****abcd"
// preview so the UI can confirm which token is stored without ever handling
// the plaintext value again.
type Project struct {
	ID                 primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Name               string             `bson:"name" json:"name"`
	Slug               string             `bson:"slug" json:"slug"`
	Description        string             `bson:"description" json:"description"`
	WebhookSecret      string             `bson:"webhook_secret" json:"-"`
	GitHubPATEncrypted []byte             `bson:"github_pat_encrypted" json:"-"`
	GitHubPATMasked    string             `bson:"github_pat_masked" json:"github_pat_masked"`
	// GitRepoURL is the project-wide repository every service clones from.
	// Each service still has GitSubpath (monorepo-friendly), but they all
	// share this single URL: the repo is cloned ONCE per project at
	// ProjectDir, so disk usage stays linear in repo size and `git pull`
	// updates every service's source in one operation.
	GitRepoURL string `bson:"git_repo_url" json:"git_repo_url"`
	// ProjectDir is /home/<user>/projects/<slug> — the on-disk root that
	// holds the project's single git clone. Stamped at provision time;
	// per-service install_dir is ProjectDir + "/" + GitSubpath.
	ProjectDir string `bson:"project_dir" json:"project_dir"`
	// User owns ProjectDir + every service's install_dir under it. Defaults
	// to the user that owns the first service's primary domain so files
	// land under the operator's existing /home/<user>/ tree instead of a
	// throwaway sp-* account.
	User               string             `bson:"user" json:"user"`
	AutoDeploy         bool               `bson:"auto_deploy" json:"auto_deploy"`
	Paused             bool               `bson:"paused" json:"paused"`
	OwnerUserID        primitive.ObjectID `bson:"owner_user_id,omitempty" json:"owner_user_id"`
	TenantID           primitive.ObjectID `bson:"tenant_id,omitempty" json:"tenant_id"`
	// LastWebhookAt is stamped on every signature-verified delivery from
	// GitHub, whether it triggered a deploy or not (ping events, paused
	// projects, pushes to non-matching branches all count). The UI uses it
	// to confirm to the operator that the webhook wiring actually works.
	LastWebhookAt *time.Time `bson:"last_webhook_at,omitempty" json:"last_webhook_at"`
	// LastWebhookEvent records the GitHub event type ("push", "ping", ...)
	// of the most recent delivery so the UI can explain *what* arrived.
	LastWebhookEvent string    `bson:"last_webhook_event,omitempty" json:"last_webhook_event"`
	CreatedAt        time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt        time.Time `bson:"updated_at" json:"updated_at"`
}

// ProjectService is a single deployable unit inside a Project. Role is one of:
//   - "backend"  — reverse-proxied to a systemd-managed process on Port
//   - "frontend" — built then served statically from BuildDir (SPA fallback)
//   - "static"   — plain HTML/CSS/JS, no build step
//
// PrimaryDomain is the canonical domain (A record to server IP). AliasDomains
// are additional domains served by the same vhost (CNAME to PrimaryDomain);
// all are added to the Let's Encrypt cert via `certbot --expand`.
//
// PathPrefix lets a backend be mounted under /api on a domain shared with a
// frontend service (empty = serves the whole server_name).
type ProjectService struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	ProjectID      primitive.ObjectID `bson:"project_id" json:"project_id"`
	Name           string             `bson:"name" json:"name"`
	Role           string             `bson:"role" json:"role"`
	Framework      string             `bson:"framework" json:"framework"`
	GitRepoURL     string             `bson:"git_repo_url" json:"git_repo_url"`
	GitSubpath     string             `bson:"git_subpath" json:"git_subpath"`
	GitBranch      string             `bson:"git_branch" json:"git_branch"`
	PathPrefix     string             `bson:"path_prefix" json:"path_prefix"`
	PrimaryDomain  string             `bson:"primary_domain" json:"primary_domain"`
	AliasDomains   []string           `bson:"alias_domains" json:"alias_domains"`
	InstallCmd     string             `bson:"install_cmd" json:"install_cmd"`
	BuildCmd       string             `bson:"build_cmd" json:"build_cmd"`
	StartCmd       string             `bson:"start_cmd" json:"start_cmd"`
	Port           int                `bson:"port" json:"port"`
	EnvVars        map[string]string  `bson:"env_vars" json:"env_vars"`
	User           string             `bson:"user" json:"user"`
	InstallDir     string             `bson:"install_dir" json:"install_dir"`
	BuildDir       string             `bson:"build_dir" json:"build_dir"`
	SystemdUnit    string             `bson:"systemd_unit" json:"systemd_unit"`
	Status         string             `bson:"status" json:"status"`
	LastCommitSHA  string             `bson:"last_commit_sha" json:"last_commit_sha"`
	LastDeployedAt *time.Time         `bson:"last_deployed_at" json:"last_deployed_at"`
	// MissingEnvKeys is populated when the cloned repo's .env.example
	// declares keys that the operator hasn't supplied via env_vars.
	// When non-empty, the deploy still installs + builds + creates the
	// systemd unit + nginx vhost, but does NOT start the service —
	// status is set to "needs_env_vars" and the WHM UI surfaces a
	// banner asking the operator to fill in the missing keys before
	// starting the service. Cleared on every successful deploy.
	MissingEnvKeys []string `bson:"missing_env_keys,omitempty" json:"missing_env_keys,omitempty"`
	CreatedAt      time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt      time.Time          `bson:"updated_at" json:"updated_at"`
}

// ProjectDeployment records a single deploy attempt (manual or webhook).
// LogPath points at the on-disk log tail we read back via GET /logs.
type ProjectDeployment struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	ProjectID  primitive.ObjectID `bson:"project_id" json:"project_id"`
	ServiceID  primitive.ObjectID `bson:"service_id" json:"service_id"`
	CommitSHA  string             `bson:"commit_sha" json:"commit_sha"`
	Trigger    string             `bson:"trigger" json:"trigger"`
	Status     string             `bson:"status" json:"status"`
	StartedAt  time.Time          `bson:"started_at" json:"started_at"`
	FinishedAt *time.Time         `bson:"finished_at" json:"finished_at"`
	LogPath    string             `bson:"log_path" json:"log_path"`
	ErrorMsg   string             `bson:"error_msg" json:"error_msg"`
	// Steps records the lifecycle of every stage in this deployment so
	// the UI can render a Transfer-style progress timeline. Always written
	// in order; the last in_progress step is the "current" one.
	Steps []DeploymentStep `bson:"steps,omitempty" json:"steps,omitempty"`
	// Progress is a 0-100 percentage derived from completed/total steps,
	// updated on every step transition.
	Progress int `bson:"progress" json:"progress"`
}

// DeploymentStep is one stage in a service's deploy: clone, install, build,
// start, vhost, ssl, health-check. Mirrors the shape of TransferStep so
// the WHM frontend can reuse the same renderer.
type DeploymentStep struct {
	Name        string     `bson:"name" json:"name"`
	Status      string     `bson:"status" json:"status"` // pending, in_progress, completed, failed, skipped
	StartedAt   *time.Time `bson:"started_at,omitempty" json:"started_at,omitempty"`
	CompletedAt *time.Time `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	Details     string     `bson:"details,omitempty" json:"details,omitempty"`
	Error       string     `bson:"error,omitempty" json:"error,omitempty"`
}

// CreateProjectRequest is the JSON body for POST /whm/projects.
type CreateProjectRequest struct {
	Name        string `json:"name" validate:"required,min=1,max=60"`
	Description string `json:"description"`
	GitHubPAT   string `json:"github_pat"`
	AutoDeploy  bool   `json:"auto_deploy"`
}

// ProvisionProjectRequest creates a project AND its services in one atomic
// call — if any service fails, the project (and any already-created services)
// are rolled back. The previous two-step flow (Create then POST services
// one-by-one) left stranded project rows when a service step crashed, which
// then broke the next retry with a unique-slug collision.
type ProvisionProjectRequest struct {
	Name        string              `json:"name" validate:"required,min=1,max=60"`
	Description string              `json:"description"`
	GitHubPAT   string              `json:"github_pat"`
	AutoDeploy  bool                `json:"auto_deploy"`
	// GitRepoURL is the project-wide repo. When supplied, every service's
	// per-request git_repo_url is overridden so all services clone from
	// this single source — the recommended layout. Backwards-compatible:
	// if GitRepoURL is empty, the wizard's per-service URLs are used as
	// before.
	GitRepoURL string              `json:"git_repo_url"`
	Services   []AddServiceRequest `json:"services" validate:"required,min=1,dive"`
}

// UpdateProjectRequest patches a Project. Empty fields are left unchanged.
type UpdateProjectRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	AutoDeploy  *bool   `json:"auto_deploy"`
	Paused      *bool   `json:"paused"`
	// GitRepoURL lets the operator move the project to a different repo
	// (e.g. fork, mirror, or rename). Backend rewrites the on-disk
	// remote URL too on the next pull so old/new URLs don't fight.
	GitRepoURL *string `json:"git_repo_url"`
}

// AddServiceRequest is the JSON body for POST /whm/projects/:id/services.
type AddServiceRequest struct {
	Name          string            `json:"name" validate:"required,min=1,max=60"`
	Role          string            `json:"role" validate:"required,oneof=backend frontend static"`
	Framework     string            `json:"framework"`
	GitRepoURL    string            `json:"git_repo_url" validate:"required"`
	GitSubpath    string            `json:"git_subpath"`
	GitBranch     string            `json:"git_branch" validate:"required"`
	PathPrefix    string            `json:"path_prefix"`
	PrimaryDomain string            `json:"primary_domain" validate:"required"`
	AliasDomains  []string          `json:"alias_domains"`
	InstallCmd    string            `json:"install_cmd"`
	BuildCmd      string            `json:"build_cmd"`
	StartCmd      string            `json:"start_cmd"`
	Port          int               `json:"port"`
	EnvVars       map[string]string `json:"env_vars"`
	User          string            `json:"user"`
}

// UpdateServiceRequest patches a ProjectService. Empty fields are left alone.
type UpdateServiceRequest struct {
	Framework  *string            `json:"framework"`
	GitBranch  *string            `json:"git_branch"`
	GitSubpath *string            `json:"git_subpath"`
	PathPrefix *string            `json:"path_prefix"`
	InstallCmd *string            `json:"install_cmd"`
	BuildCmd   *string            `json:"build_cmd"`
	StartCmd   *string            `json:"start_cmd"`
	Port       *int               `json:"port"`
	EnvVars    *map[string]string `json:"env_vars"`
}

// AddAliasRequest is the JSON body for POST /services/:svc/aliases.
type AddAliasRequest struct {
	Domain string `json:"domain" validate:"required"`
}

// RotatePATRequest is the JSON body for POST /projects/:id/rotate-pat.
type RotatePATRequest struct {
	GitHubPAT string `json:"github_pat" validate:"required"`
}
