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
	// GitBranch is the project-wide branch every service tracks. Hoisted
	// from the per-service ProjectService.GitBranch field as of 3.1.27 —
	// in the new shared-clone layout (one .git per project), services
	// SHARE one working tree so they can't legitimately track different
	// branches. The previous per-service field stays around for back-
	// compat reads on legacy projects (loadProject auto-heals empty
	// Project.GitBranch from the first service's value on first read).
	// Empty on a freshly-imported legacy project until the heal fires.
	GitBranch string `bson:"git_branch" json:"git_branch"`
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
	LastWebhookEvent string `bson:"last_webhook_event,omitempty" json:"last_webhook_event"`
	// LastWebhookError + LastWebhookErrorAt record the most recent FAILED
	// delivery (signature mismatch, malformed payload, missing secret).
	// Pre-3.1.73 these were invisible — the panel returned 200 + ignored
	// to GitHub (so retries didn't pile up) and stamped nothing, so the
	// "Waiting for first delivery" badge stayed up forever even when
	// GitHub was actively delivering. Now the operator sees "Signature
	// mismatch · 2m ago" and knows to re-paste the secret into GitHub.
	// Cleared on the next successful verification so a one-off mismatch
	// during secret-rotation doesn't haunt the UI forever.
	LastWebhookError   string     `bson:"last_webhook_error,omitempty" json:"last_webhook_error"`
	LastWebhookErrorAt *time.Time `bson:"last_webhook_error_at,omitempty" json:"last_webhook_error_at"`
	CreatedAt          time.Time  `bson:"created_at" json:"created_at"`
	UpdatedAt          time.Time  `bson:"updated_at" json:"updated_at"`
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
	// RuntimeVersion pins the language runtime (Node / Go / Ruby / Python)
	// to a specific installed version. Empty = use the system default that
	// resolves from PATH. Populated from the operator's dropdown, which is
	// itself populated from GET /software/runtimes (only versions actually
	// installed on the host are offered, so this never names a missing bin).
	RuntimeVersion string            `bson:"runtime_version" json:"runtime_version"`
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

	// SSLWarning is a transient (`bson:"-"`) human-readable string set
	// by AddAliasWithProject / RemoveAliasWithProject when nginx +
	// vhost reconcile succeeded but the Let's Encrypt cert expansion
	// failed (e.g. DNS for the new alias doesn't yet point at this
	// box). The alias is still persisted in alias_domains and listed
	// in the vhost server_name — once DNS lands the operator can hit
	// "Reissue" on the SSL page and the cert will pick the alias up
	// without re-running the link flow. Pre-3.1.31 this failure mode
	// was stderr-only and the API returned 200 + an unmodified
	// service object, leaving integrators no signal at all that
	// `https://<new-alias>` would serve a wrong cert.
	SSLWarning string `bson:"-" json:"ssl_warning,omitempty"`
	// SSLCoveredDomains is the post-reconcile SAN list of the cert
	// for PrimaryDomain (parsed via `openssl x509 -text`). Populated
	// alongside SSLWarning so an API consumer can verify that the
	// cert actually covers the alias they just linked without
	// shelling into the box.
	SSLCoveredDomains []string `bson:"-" json:"ssl_covered_domains,omitempty"`
	// AttachedDomains is the durable v3.1.114 replacement for AliasDomains:
	// the list of registered domains whose Domain.proxy_service_id points
	// at THIS service (each serving the service via its own reverse-proxy
	// vhost + cert). Transient (`bson:"-"`) — populated at read time from
	// the domains collection, so it can never be clobbered by a service
	// edit the way the old alias_domains array was. The panel renders the
	// service's domain list from this field; AliasDomains is kept only for
	// back-compat reads of un-migrated rows.
	AttachedDomains []string `bson:"-" json:"attached_domains,omitempty"`
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
	// 3.1.86 — transient enrichment fields. NOT persisted (bson:"-")
	// because the source of truth for these is the ProjectService row
	// keyed by ServiceID, and storing them on every deployment record
	// would either bloat the DB or go stale when a service is renamed.
	// ProjectService.Activity() populates them in-memory after the
	// recent_deployments find so the UI can render a "service name"
	// chip on each row without a second round-trip.
	ServiceName string `bson:"-" json:"service_name,omitempty"`
	ServiceRole string `bson:"-" json:"service_role,omitempty"`
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
	// GitBranch is the project-wide branch every service tracks.
	// Optional on the wire; defaults to "main" at the service layer.
	GitBranch string `json:"git_branch"`
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
	// GitBranch is the project-wide branch every service tracks. As of
	// 3.1.27 this is the source of truth; per-service git_branch on the
	// wire is honoured for back-compat but the project-level value
	// wins when both are set, and on Provision we propagate this value
	// down to every service row so the resulting on-disk + DB state
	// stays internally consistent.
	GitBranch string `json:"git_branch"`
	// User pins the project (and every service) to a specific system user
	// account — projects/services land under /home/<user>/projects/<slug>/
	// and `git pull` runs as that user. Optional; when blank the backend
	// derives it from the first service's primary domain owner.
	User     string              `json:"user"`
	Services []AddServiceRequest `json:"services" validate:"required,min=1,dive"`
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
	// GitBranch lets the operator change the tracked branch
	// project-wide. Next deploy pulls from origin/<new-branch> on the
	// shared clone; per-service rows have their git_branch field
	// updated in the same transaction so legacy consumers stay in
	// sync with the project-level source of truth.
	GitBranch *string `json:"git_branch"`
}

// AddServiceRequest is the JSON body for POST /whm/projects/:id/services.
//
// GitRepoURL is intentionally NOT marked `validate:"required"` at the
// payload level — every service in a project shares the project's
// repo URL (set when the project was created), so ProjectService.AddService
// inherits from the parent project when the request omits it. The
// field is still on the wire so the legacy split-repo flow keeps
// working for projects created before the shared-clone refactor.
// AddService returns a clear error if BOTH the request and project
// are empty, so the operator never gets a silent no-op clone.
type AddServiceRequest struct {
	Name           string            `json:"name" validate:"required,min=1,max=60"`
	Role           string            `json:"role" validate:"required,oneof=backend frontend static"`
	Framework      string            `json:"framework"`
	GitRepoURL     string            `json:"git_repo_url"`
	GitSubpath     string            `json:"git_subpath"`
	// GitBranch is now optional at the per-service level — as of
	// 3.1.27 the project carries the canonical branch and Provision /
	// AddService propagate it down. A non-empty value here still
	// wins for the rare case of a multi-branch monorepo split that
	// pre-dates the hoist.
	GitBranch string `json:"git_branch"`
	PathPrefix     string            `json:"path_prefix"`
	PrimaryDomain  string            `json:"primary_domain" validate:"required"`
	AliasDomains   []string          `json:"alias_domains"`
	InstallCmd     string            `json:"install_cmd"`
	BuildCmd       string            `json:"build_cmd"`
	StartCmd       string            `json:"start_cmd"`
	RuntimeVersion string            `json:"runtime_version"`
	Port           int               `json:"port"`
	EnvVars        map[string]string `json:"env_vars"`
	User           string            `json:"user"`
}

// UpdateServiceRequest patches a ProjectService. Nil fields are left alone.
//
// PrimaryDomain and AliasDomains use pointer/slice indirection so the
// caller can distinguish "field omitted" (leave as-is) from "field set
// to empty" (clear all aliases / no-op for an empty primary, which is
// rejected). Editing PrimaryDomain triggers a full vhost rename: the
// old vhost file is unlinked, the new primary's vhost is written with
// the merged alias list, and IssueLetsEncryptMulti runs against the
// new --cert-name. The old cert is left in place — Let's Encrypt
// rate-limits revocations and the unused cert simply expires.
type UpdateServiceRequest struct {
	Framework      *string            `json:"framework"`
	GitBranch      *string            `json:"git_branch"`
	GitSubpath     *string            `json:"git_subpath"`
	PathPrefix     *string            `json:"path_prefix"`
	InstallCmd     *string            `json:"install_cmd"`
	BuildCmd       *string            `json:"build_cmd"`
	StartCmd       *string            `json:"start_cmd"`
	RuntimeVersion *string            `json:"runtime_version"`
	Port           *int               `json:"port"`
	EnvVars        *map[string]string `json:"env_vars"`
	PrimaryDomain  *string            `json:"primary_domain"`
	AliasDomains   *[]string          `json:"alias_domains"`
}

// AddAliasRequest is the JSON body for POST /services/:svc/aliases.
type AddAliasRequest struct {
	Domain string `json:"domain" validate:"required"`
}

// RotatePATRequest is the JSON body for POST /projects/:id/rotate-pat.
type RotatePATRequest struct {
	GitHubPAT string `json:"github_pat" validate:"required"`
}

// ProjectExport is the portable JSON shape produced by GET
// /whm/projects/:id/export and consumed by POST /whm/projects/import.
//
// Design goals:
//
//   - Portable across panel installs. Carries ONLY config that's meaningful
//     on a fresh box: repo URL, branch, framework, domains, env vars,
//     commands. Excludes everything host-specific (project_dir, install_dir,
//     systemd_unit, mongo ObjectIDs, the encrypted PAT cipher — which is
//     sealed under THIS panel's APP_ENCRYPTION_KEY and would not decrypt on
//     a different box), per-instance state (last_commit_sha, last_webhook_at,
//     last_deployed_at, deployment history), and ephemeral runtime fields
//     (status, ssl_warning, missing_env_keys).
//
//   - Distinct from the server-to-server transfer pipeline. The transfer
//     uses raw mongoexport JSON of every collection at once and is meant
//     to clone a whole panel; this export is one project, intended for
//     templating / backup / sharing / cross-panel cloning.
//
//   - GitHub PAT is NEVER included in the export. Operator pastes a fresh
//     PAT during import if the repo is private.
//
//   - Webhook secret is NEVER included. A fresh one is minted on import.
//
//   - Schema-versioned so future field additions stay readable by older
//     panels (which skip unknown fields) and older exports stay importable
//     by newer panels (which set defaults for missing fields).
type ProjectExport struct {
	SchemaVersion int                 `json:"schema_version"`
	ExportedAt    string              `json:"exported_at"`
	PanelVersion  string              `json:"panel_version"`
	Project       ExportProjectInfo   `json:"project"`
	Services      []ExportServiceInfo `json:"services"`
}

// ExportProjectInfo is the project-level slice of ProjectExport. Mirrors the
// fields the New Project wizard captures, minus the PAT (security) and the
// webhook secret (regenerated on import).
type ExportProjectInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	GitRepoURL  string `json:"git_repo_url"`
	GitBranch   string `json:"git_branch"`
	AutoDeploy  bool   `json:"auto_deploy"`
	// User is the system account that owns ProjectDir on the SOURCE panel.
	// Optional on import — when the destination has no matching user the
	// service layer's user-derivation kicks in (first service's primary
	// domain owner, then synthetic sp-<slug>-<hash>). Keeping the field
	// in the export lets a clone-onto-same-panel import preserve the
	// account it came from.
	User string `json:"user,omitempty"`
}

// ExportServiceInfo is the per-service slice. Domains are included verbatim,
// but the import flow lets the operator rewrite them via OverrideDomains so
// cloning onto the same panel doesn't collide on the globally-unique domain
// constraint.
type ExportServiceInfo struct {
	Name           string            `json:"name"`
	Role           string            `json:"role"`
	Framework      string            `json:"framework,omitempty"`
	GitSubpath     string            `json:"git_subpath,omitempty"`
	GitBranch      string            `json:"git_branch,omitempty"`
	PathPrefix     string            `json:"path_prefix,omitempty"`
	PrimaryDomain  string            `json:"primary_domain"`
	AliasDomains   []string          `json:"alias_domains,omitempty"`
	InstallCmd     string            `json:"install_cmd,omitempty"`
	BuildCmd       string            `json:"build_cmd,omitempty"`
	StartCmd       string            `json:"start_cmd,omitempty"`
	RuntimeVersion string            `json:"runtime_version,omitempty"`
	Port           int               `json:"port,omitempty"`
	EnvVars        map[string]string `json:"env_vars,omitempty"`
}

// ImportProjectRequest is the JSON body for POST /whm/projects/import. The
// import shape wraps the raw manifest so the operator can pass an optional
// fresh GitHub PAT (private repos) and override fields that would collide
// on the destination panel (project name uniqueness, domain uniqueness)
// in one round-trip — no second "rename then retry" step.
type ImportProjectRequest struct {
	Manifest  ProjectExport `json:"manifest" validate:"required"`
	GitHubPAT string        `json:"github_pat"`
	// OverrideName replaces Manifest.Project.Name when set. Useful when
	// importing the same manifest twice onto one panel — the second
	// import has to land under a distinct project name because the slug
	// (derived from name) has a unique index.
	OverrideName string `json:"override_name"`
	// OverrideDomains rewrites primary_domain on each service. Keyed by
	// the manifest's original primary_domain so the operator can
	// remap dev.api.example.com → staging.api.example.com without
	// matching by index. Missing keys fall through to the manifest value.
	// Alias domains are NOT rewritten by this map (they're optional and
	// the operator can clear them in the panel after import).
	OverrideDomains map[string]string `json:"override_domains"`
	// User overrides Manifest.Project.User when set. Same purpose as the
	// New Project wizard's User field: pin the project to a specific
	// system account on the destination panel.
	User string `json:"user"`
}

// CurrentProjectExportSchemaVersion is the version stamped into every
// freshly-generated export. Bump when ProjectExport's shape gains a field
// that older panels could misinterpret (renames, type changes); leave
// alone for purely additive optional fields that older panels can ignore.
const CurrentProjectExportSchemaVersion = 1
