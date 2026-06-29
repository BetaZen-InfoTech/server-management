package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/crypto"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/githubsig"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/version"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Sentinel errors surfaced by the alias-link path (AddAlias / RemoveAlias
// + their *WithProject scoped wrappers). Handlers translate these to
// HTTP status codes via errors.Is so the API distinguishes "no such
// resource" (404) from "you can't touch that" (403) — pre-3.1.30 every
// failure landed as a flat 400 BadRequest, which made debugging an
// "API integrator points at the wrong project ID" indistinguishable
// from "API integrator forgot to send the body".
var (
	// ErrServiceNotFound — :svc path param doesn't resolve to a row.
	ErrServiceNotFound = errors.New("service not found")
	// ErrProjectNotFound — :id path param doesn't resolve to a row.
	ErrProjectNotFound = errors.New("project not found")
	// ErrServiceProjectMismatch — :svc resolves but lives under a different
	// project than the :id path param. Treated as 403, not 404, because the
	// caller managed to NAME a real service — they just lacked the right
	// to reach it through this URL. 404 would leak that the service exists
	// elsewhere.
	ErrServiceProjectMismatch = errors.New("service does not belong to this project")
	// ErrCrossTenantProject — caller's tenant doesn't own the project.
	// Pre-3.1.30 a vendor with a deploy:link token could mutate any
	// other tenant's service just by guessing the ObjectID.
	ErrCrossTenantProject = errors.New("project belongs to a different tenant")
	// ErrLinkedDomainNotOwned — caller's tenant doesn't own the linked
	// domain. Mirrors the panel rule that every domain action is scoped
	// to the caller's tenant — without it a vendor could "claim" a
	// stranger's domain by linking it as an alias on their own service.
	ErrLinkedDomainNotOwned = errors.New("linked domain is not in your tenant")
)

// assertCanLinkAliasOnService is the one-stop tenant guard for both
// AddAlias and RemoveAlias. Returns the resolved service + project so
// the caller doesn't have to re-fetch.
//
//   - svcIDHex: the :svc path param. Required.
//   - projectIDHex: the :id path param. Optional ("" skips the
//     consistency check). Both panel + programmatic callers pass it
//     post-3.1.30; legacy in-process callers that only have a service
//     ID (e.g. transfer-time replays) pass empty.
//   - domain: the alias being added or removed. Required for AddAlias
//     (we need to assert the caller owns it) — RemoveAlias may pass
//     it as an idempotent best-effort drop, so missing-domain ownership
//     short-circuits to "ok" instead of failing.
//   - mustOwnDomain: AddAlias passes true, RemoveAlias false.
//
// Tenant scoping is conditional: vendor_owner / nil scope (internal
// callers) bypass every check. Tenant-scoped roles (vendor_admin,
// vendor_staff, customer, etc., AND every API token whose pinned vendor
// resolves to a non-owner role) hit the full set.
func (s *ProjectService) assertCanLinkAliasOnService(ctx context.Context, projectIDHex, svcIDHex, domain string, mustOwnDomain bool) (*models.ProjectService, *models.Project, error) {
	svcOID, err := primitive.ObjectIDFromHex(svcIDHex)
	if err != nil {
		return nil, nil, ErrServiceNotFound
	}
	var svc models.ProjectService
	if err := s.db.Collection(database.ColProjectServices).FindOne(ctx, bson.M{"_id": svcOID}).Decode(&svc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil, ErrServiceNotFound
		}
		return nil, nil, err
	}

	if projectIDHex != "" {
		projOID, err := primitive.ObjectIDFromHex(projectIDHex)
		if err != nil {
			return nil, nil, ErrProjectNotFound
		}
		if svc.ProjectID != projOID {
			return nil, nil, ErrServiceProjectMismatch
		}
	}

	proj, err := s.loadProject(ctx, svc.ProjectID)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil, ErrProjectNotFound
		}
		return nil, nil, err
	}

	scope := GetCallerScope(ctx)
	if scope != nil && constants.IsTenantScoped(scope.Role) {
		if scope.TenantHex == "" || proj.TenantID.IsZero() || proj.TenantID.Hex() != scope.TenantHex {
			return nil, nil, ErrCrossTenantProject
		}
		if mustOwnDomain && domain != "" {
			if err := scope.AssertOwnsDomain(ctx, s.db, domain); err != nil {
				return nil, nil, ErrLinkedDomainNotOwned
			}
		}
	}
	return &svc, proj, nil
}

// ProjectService manages Deploy Software projects: the logical wrapper around
// one or more deployable services (backend APIs, frontend SPAs, static sites)
// that share a GitHub PAT, a webhook secret, and a single point of
// multi-domain SSL management.
//
// Deploys run on a small worker pool (see startWorker) so the HTTP handlers
// that trigger them can return quickly; callers who need the final status
// poll /services/:svc for the updated LastCommitSHA and LastDeployedAt.
type ProjectService struct {
	db              *mongo.Database
	encKey          []byte
	webhookBaseURL  string
	sslEmail        string
	serverIP        string

	// deployQueue is drained by the worker pool in background goroutines.
	deployQueue chan deployJob

	// certLocks serialises certbot --expand calls per primary domain; two
	// concurrent expansions on the same cert file corrupt it.
	certLocks sync.Map

	// gitLocks serialises git operations per project clone dir. Two
	// concurrent `git fetch + reset --hard` on the same dir would race
	// and can leave the working tree in a half-applied state — so each
	// inPlaceSync grabs the mutex for its target dir before touching git.
	// Keyed by the absolute gitOpsDir path.
	gitLocks sync.Map
}

type deployJob struct {
	serviceID primitive.ObjectID
	trigger   string
	// skipPull true → runDeploy marks the "Pull source from Git" step as
	// skipped and goes straight to install/build/restart. Used by the
	// per-service Redeploy button (operator wants to rebuild + restart
	// the existing on-disk source without fetching new commits — that's
	// what the project-level Pull button is for).
	skipPull bool
	// projectPullCommit (v3.1.69) is set by runProjectPullAndEnqueue
	// when a successful project-level git pull just landed on this
	// commit at proj.ProjectDir. runDeploy reads it to render the
	// per-service "Pull source from Git" step as COMPLETED (not
	// skipped) with the actual SHA + a shared-clone explainer, so
	// the operator's per-service progress strip stops claiming "no
	// pull happened" when in fact the project-level pull just ran.
	// Empty for per-service Redeploy clicks (skipPull stays true,
	// projectPullCommit stays "") so the original skipped-step UX
	// is preserved for that case.
	projectPullCommit string
}

// NewProjectService wires up the dependencies and starts the deploy worker
// pool.
//
// `workers` controls how many service deploys run in parallel across the
// whole panel. Passed in from cfg.DeployWorkers (env DEPLOY_WORKERS),
// default 4. Clamped to [1, 32] — 1 forces serial deploys, 32 is more
// than any single-box panel can usefully sustain (each worker can run
// a `npm install` + build, which saturates CPU + IO at much smaller
// counts). 0 or negative falls back to the 4 default so a missing env
// doesn't yield zero workers and an immobile queue.
//
// Pre-3.1.86 this was hardcoded at 2. Operators on bigger boxes with
// 7+ service projects (Hospital Dev, Waapi Dev 3.0) saw queue-full /
// pending stalls because only 2 services deployed at a time after a
// webhook fan-out; the remaining N-2 waited their turn behind a
// fundamentally CPU-bound build pipeline.
//
// Queue buffer raised from 64 to 256 so a project-wide deploy of a
// 20-service monorepo doesn't fill the channel mid-fan-out and trip
// the "queue-full" backstop on the tail rows.
func NewProjectService(db *mongo.Database, encKey []byte, webhookBaseURL, sslEmail, serverIP string, workers int) *ProjectService {
	if workers <= 0 {
		workers = 4
	}
	if workers > 32 {
		workers = 32
	}
	s := &ProjectService{
		db:             db,
		encKey:         encKey,
		webhookBaseURL: strings.TrimRight(webhookBaseURL, "/"),
		sslEmail:       sslEmail,
		serverIP:       serverIP,
		deployQueue:    make(chan deployJob, 256),
	}
	for i := 0; i < workers; i++ {
		go s.startWorker()
	}
	log.Info().Int("workers", workers).Int("queue_buffer", 256).Msg("project deploy pool started")
	return s
}

// slugPattern is used to normalise project names into URL/systemd-safe slugs.
var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugPattern.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// generateWebhookSecret returns a 64-character hex string. 32 bytes of entropy
// is plenty for HMAC-SHA256 and fits comfortably in GitHub's secret field.
func generateWebhookSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Create persists a new Project record. The PAT is encrypted at rest and a
// fresh per-project webhook secret is generated. The returned object has
// GitHubPATEncrypted zeroed so it never travels back through JSON.
//
// If the slug derived from the name collides with an existing project, the
// insert is retried with `-2`, `-3`, ... up to -50 before giving up. This
// keeps retries-after-partial-failure working without forcing operators to
// change their preferred project name.
func (s *ProjectService) Create(ctx context.Context, req *models.CreateProjectRequest) (*models.Project, error) {
	baseSlug := slugify(req.Name)
	if baseSlug == "" {
		return nil, fmt.Errorf("project name must contain at least one alphanumeric character")
	}

	secret, err := generateWebhookSecret()
	if err != nil {
		return nil, err
	}

	branch := strings.TrimSpace(req.GitBranch)
	if branch == "" {
		branch = "main"
	}
	proj := models.Project{
		Name:          strings.TrimSpace(req.Name),
		Description:   req.Description,
		WebhookSecret: secret,
		AutoDeploy:    req.AutoDeploy,
		GitBranch:     branch,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if pat := strings.TrimSpace(req.GitHubPAT); pat != "" {
		enc, err := crypto.EncryptGCM([]byte(pat), s.encKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt PAT: %w", err)
		}
		proj.GitHubPATEncrypted = enc
		proj.GitHubPATMasked = crypto.MaskToken(pat)
	}

	if scope := GetCallerScope(ctx); scope != nil {
		if tid, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
			proj.TenantID = tid
		}
		if uid, err := primitive.ObjectIDFromHex(scope.UserHex); err == nil {
			proj.OwnerUserID = uid
		}
	}

	col := s.db.Collection(database.ColProjects)
	// Try baseSlug, then baseSlug-2, baseSlug-3, ... to sidestep unique-index
	// collisions from abandoned retries. Cap at 50 so a truly stuck database
	// doesn't spin forever.
	for attempt := 1; attempt <= 50; attempt++ {
		proj.Slug = baseSlug
		if attempt > 1 {
			proj.Slug = fmt.Sprintf("%s-%d", baseSlug, attempt)
		}
		result, err := col.InsertOne(ctx, proj)
		if err == nil {
			proj.ID = result.InsertedID.(primitive.ObjectID)
			proj.GitHubPATEncrypted = nil
			proj.WebhookSecret = ""
			return &proj, nil
		}
		if !mongo.IsDuplicateKeyError(err) {
			return nil, err
		}
		// Dup key — next iteration tries the next numbered slug.
	}
	return nil, fmt.Errorf("could not allocate a unique slug for %q after 50 attempts", baseSlug)
}

// Provision creates a project plus every service in one logical transaction.
// If any step fails, the partially-constructed project is fully rolled back
// (services removed, nginx vhosts deleted, on-disk code wiped, project row
// deleted) so retries don't hit a unique-slug collision on a stranded row.
//
// Returns the created project with its services attached for the UI to
// render without a second round trip.
type ProvisionResult struct {
	Project  *models.Project          `json:"project"`
	Services []models.ProjectService `json:"services"`
}

func (s *ProjectService) Provision(ctx context.Context, req *models.ProvisionProjectRequest) (*ProvisionResult, error) {
	if len(req.Services) == 0 {
		return nil, fmt.Errorf("at least one service is required")
	}
	// Project-level repo URL takes precedence over per-service URLs — the
	// frontend wizard now collects it on Step 1 and stamps every service
	// with the same value. New layout requires it; back-compat path that
	// reads per-service git_repo_url is preserved for legacy API callers.
	repoURL := strings.TrimRight(strings.TrimSpace(req.GitRepoURL), "/")
	if repoURL != "" {
		// Basic shape check — protect against typos before we spawn a
		// failed git clone with a confusing error.
		if !strings.HasPrefix(repoURL, "https://") && !strings.HasPrefix(repoURL, "git@") {
			return nil, fmt.Errorf("git_repo_url must be an https:// URL (or git@ SSH) — got %q", repoURL)
		}
		for i := range req.Services {
			req.Services[i].GitRepoURL = repoURL
		}
	} else {
		// Back-compat: at least one service must carry a per-service URL.
		hasAny := false
		for i := range req.Services {
			if strings.TrimSpace(req.Services[i].GitRepoURL) != "" {
				hasAny = true
				break
			}
		}
		if !hasAny {
			return nil, fmt.Errorf("git_repo_url is required (set the project-level Repository URL on the wizard's Basics step)")
		}
	}

	// Project-level branch hoist (3.1.27). The wizard now collects ONE
	// branch on the project setup step; every service inherits it.
	// Propagate to every service request so the per-row git_branch
	// stays consistent with the new Project.GitBranch field.
	//
	// Back-compat fallback: when the request omits git_branch at the
	// project level (legacy API caller, programmatic transfer payload
	// from a pre-3.1.27 source), derive from the first non-empty
	// service branch — same value the operator originally typed in
	// the wizard. Last resort: "main".
	branch := strings.TrimSpace(req.GitBranch)
	if branch == "" {
		for i := range req.Services {
			if b := strings.TrimSpace(req.Services[i].GitBranch); b != "" {
				branch = b
				break
			}
		}
	}
	if branch == "" {
		branch = "main"
	}
	for i := range req.Services {
		req.Services[i].GitBranch = branch
	}
	req.GitBranch = branch

	proj, err := s.Create(ctx, &models.CreateProjectRequest{
		Name:        req.Name,
		Description: req.Description,
		GitHubPAT:   req.GitHubPAT,
		AutoDeploy:  req.AutoDeploy,
		GitBranch:   branch,
	})
	if err != nil {
		return nil, err
	}

	// Pick the project's user up front so it's pinned regardless of which
	// layout (shared clone vs per-service repo) applies. Without this, the
	// per-service-URL path left projects.user="" in mongo — which made
	// the server-transfer sync skip the whole project (it filters by user).
	projectUser := strings.TrimSpace(req.User)
	if projectUser == "" {
		if owner := s.lookupDomainOwner(ctx, req.Services[0].PrimaryDomain); owner != "" {
			projectUser = owner
		} else {
			projectUser = defaultProjectUser(proj.Slug)
		}
	}

	// Re-stamp the project's tenant_id + owner_user_id to match the
	// user the project was provisioned FOR (vs. the WHM admin who
	// pressed Create). Create() captured the admin's scope, so a
	// project the platform owner provisions on behalf of a vendor
	// would otherwise carry the OWNER's tenant_id and never appear
	// in that vendor's User Panel — the cpanel project list filters
	// strictly on tenant_id == caller_tenant. Re-pointing here
	// re-routes the project to the owning vendor's tenant. Helper
	// no-ops cleanly when projectUser doesn't resolve to a real
	// User row (the synthetic sp-<slug>-<hash> fallback case).
	s.assignProjectOwnership(ctx, proj, projectUser)

	// If a project-wide repo URL was supplied, create the SHARED clone
	// once at /home/<user>/projects/<slug>/. Each service's install_dir
	// will be a subdirectory inside that clone (named after its
	// GitSubpath), so a single `git pull` updates every service's source
	// in one operation and disk usage stays linear in repo size.
	if repoURL != "" {
		if err := ensureUser(ctx, projectUser); err != nil {
			_ = s.Delete(context.Background(), proj.ID.Hex())
			return nil, fmt.Errorf("ensure project user: %w", err)
		}
		projectDir := fmt.Sprintf("/home/%s/projects/%s", projectUser, proj.Slug)
		// Default branch for the initial clone — first service's branch.
		// Each service can still check out a different branch later if
		// monorepo workflow demands it (e.g. via git worktree).
		defaultBranch := strings.TrimSpace(req.Services[0].GitBranch)
		if defaultBranch == "" {
			defaultBranch = "main"
		}
		// Use the plaintext PAT from the request rather than reloading the
		// just-created project — Create() sanitises GitHubPATEncrypted to
		// nil on the returned struct (so it never leaks back through API
		// responses), which would make decryptPAT return an empty token
		// and cause a 'could not read Username' clone failure for private
		// repos. The plaintext is what we just stored encrypted, so it's
		// the same token.
		token := strings.TrimSpace(req.GitHubPAT)
		if err := s.cloneProjectRepo(ctx, projectDir, projectUser, repoURL, defaultBranch, token); err != nil {
			// Hard-clean any partial state before rolling back the project
			// row — Delete won't soft-delete projectDir because we haven't
			// written it to the DB yet, so leftover files would leak.
			agent.RunCommand(context.Background(), "rm", "-rf", projectDir, projectDir+".src", projectDir+".reclone")
			_ = s.Delete(context.Background(), proj.ID.Hex())
			return nil, fmt.Errorf("project clone failed: %s", sanitiseGitError(err, token))
		}
		// Persist the new project-level fields so AddService below can pick
		// the new layout via proj.ProjectDir != "".
		s.db.Collection(database.ColProjects).UpdateOne(ctx, bson.M{"_id": proj.ID}, bson.M{
			"$set": bson.M{
				"git_repo_url": repoURL,
				"git_branch":   branch,
				"project_dir":  projectDir,
				"user":         projectUser,
				"updated_at":   time.Now(),
			},
		})
		proj.GitRepoURL = repoURL
		proj.GitBranch = branch
		proj.ProjectDir = projectDir
		proj.User = projectUser
	} else {
		// Per-service-URL layout — still persist user (RBAC + transfer
		// sync) and the project-level branch (3.1.27) so legacy
		// projects also carry the canonical branch on the project row.
		s.db.Collection(database.ColProjects).UpdateOne(ctx, bson.M{"_id": proj.ID}, bson.M{
			"$set": bson.M{
				"user":       projectUser,
				"git_branch": branch,
				"updated_at": time.Now(),
			},
		})
		proj.User = projectUser
		proj.GitBranch = branch
	}

	services := make([]models.ProjectService, 0, len(req.Services))
	for i := range req.Services {
		// Default each service to the project's user so AddService doesn't
		// auto-pick a different user per service (which would break the
		// shared-clone layout — the project user owns proj.ProjectDir).
		if proj.User != "" && req.Services[i].User == "" {
			req.Services[i].User = proj.User
		}
		svc, err := s.AddService(ctx, proj.ID.Hex(), &req.Services[i])
		if err != nil {
			// Full rollback: Delete cascades through every service created
			// so far (nginx, systemd, files) and then deletes the project.
			_ = s.Delete(context.Background(), proj.ID.Hex())
			// Preserve typed BuildError so the HTTP handler can return 422
			// with the ANSI-stripped details payload. Other errors stay
			// plain and get mapped to 500.
			if be, ok := err.(*BuildError); ok {
				return nil, &ProvisionError{
					ServiceName: req.Services[i].Name,
					Build:       be,
				}
			}
			return nil, fmt.Errorf("service %q: %w", req.Services[i].Name, err)
		}
		services = append(services, *svc)
	}
	return &ProvisionResult{Project: proj, Services: services}, nil
}

// cloneProjectRepo performs the initial single shared clone for a project
// when GitRepoURL is set on Provision. The clone goes into projectDir and
// owns the .git directory for every subsequent `git pull` (one pull
// updates every service's source).
//
// Idempotent + non-destructive on retries:
//   - If projectDir doesn't exist: clone normally to a temp dir then
//     promote into place (atomic, so a partial clone never leaves files
//     at projectDir).
//   - If projectDir exists EMPTY (leftover from a previous failed clone):
//     hard-delete the empty dir and clone fresh.
//   - If projectDir exists WITH FILES (operator uploaded something or a
//     previous clone partially succeeded): use inPlaceSync to add a remote,
//     fetch, and reset --hard onto it. Tracked files are overwritten;
//     untracked uploads are preserved. NO rename.
func (s *ProjectService) cloneProjectRepo(ctx context.Context, projectDir, user, repoURL, branch, token string) error {
	if projectDir == "" {
		return fmt.Errorf("projectDir required")
	}
	dirExists := false
	if _, err := agent.RunCommand(ctx, "test", "-e", projectDir); err == nil {
		dirExists = true
	}
	if dirExists {
		// Empty dir is just a stale leftover — hard-delete and clone fresh
		// is fine, no user data at risk.
		isEmpty := true
		if r, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("ls -A %s 2>/dev/null | head -1", projectDir)); err == nil && r != nil && strings.TrimSpace(r.Output) != "" {
			isEmpty = false
		}
		if isEmpty {
			agent.RunCommand(ctx, "rm", "-rf", projectDir)
			dirExists = false
		} else {
			// Has files — use in-place sync (no rename, preserves uploads).
			syncErr, _, _ := s.inPlaceSync(ctx, projectDir, repoURL, branch, token, user)
			if syncErr != nil {
				return fmt.Errorf("in-place sync of existing projectDir: %w", syncErr)
			}
			if user != "" {
				if err := chownRecursive(ctx, projectDir, user); err != nil {
					return fmt.Errorf("chown project clone: %w", err)
				}
			}
			return nil
		}
	}
	if _, err := agent.RunCommand(ctx, "mkdir", "-p", projectDir); err != nil {
		return fmt.Errorf("mkdir projectDir: %w", err)
	}
	tmp := projectDir + ".src"
	agent.RunCommand(ctx, "rm", "-rf", tmp)
	cloneCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	cloneErr := agent.GitClone(cloneCtx, repoURL, branch, tmp, token)
	cancel()
	if cloneErr != nil {
		// Don't leave an empty projectDir behind — it confuses retries.
		agent.RunCommand(ctx, "rm", "-rf", tmp, projectDir)
		return cloneErr
	}
	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("shopt -s dotglob && mv %s/* %s/ && rm -rf %s", tmp, projectDir, tmp)); err != nil {
		return fmt.Errorf("stage project clone: %w", err)
	}
	if user != "" {
		if err := chownRecursive(ctx, projectDir, user); err != nil {
			return fmt.Errorf("chown project clone: %w", err)
		}
	}
	return nil
}

// ProvisionError is the typed error returned from Provision when a specific
// service fails its build step. The HTTP handler unwraps this to a 422
// response with the full (ANSI-stripped) build output in the details field.
type ProvisionError struct {
	ServiceName string
	Build       *BuildError
}

func (e *ProvisionError) Error() string {
	return fmt.Sprintf("service %q: %s", e.ServiceName, e.Build.Error())
}

// Export builds a portable ProjectExport manifest for the given project. The
// returned struct excludes every host-specific field (project_dir, install_dir,
// systemd_unit, mongo _ids), every secret (encrypted PAT cipher — sealed under
// THIS panel's APP_ENCRYPTION_KEY and undecryptable elsewhere; webhook_secret —
// regenerated on import to keep the GitHub-side config aligned with a single
// owner), and every piece of per-instance runtime state (last_commit_sha,
// last_webhook_at, last_deployed_at, status). What's left is what the New
// Project wizard would have asked the operator for: repo URL, branch,
// per-service framework + commands + domains + env vars + port.
//
// Use cases:
//   - Template a project on a development panel and replay it onto production.
//   - Snapshot config for backup before risky edits.
//   - Move ONE project between panels (the full server-transfer pipeline
//     moves an entire panel; this export is the single-project analogue).
//   - Share a deployment recipe with a teammate without exposing the PAT.
//
// Disjoint from the transfer pipeline: that path uses raw mongoexport of every
// collection and depends on idMap to remint ObjectIDs in lockstep across
// cross-cutting refs (tenant_id, owner_user_id, project_id). Export here
// produces a self-contained per-project document that the destination panel
// can ingest via the same Provision path used by the manual wizard — no
// idMap, no cross-collection refs, no risk of polluting the transfer state.
func (s *ProjectService) Export(ctx context.Context, projectID string) (*models.ProjectExport, error) {
	oid, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		return nil, fmt.Errorf("invalid project id")
	}
	proj, err := s.loadProject(ctx, oid)
	if err != nil {
		return nil, fmt.Errorf("project not found")
	}
	svcs, err := s.listServicesForProject(ctx, oid)
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}

	out := &models.ProjectExport{
		SchemaVersion: models.CurrentProjectExportSchemaVersion,
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		PanelVersion:  version.Number(),
		Project: models.ExportProjectInfo{
			Name:        proj.Name,
			Description: proj.Description,
			GitRepoURL:  proj.GitRepoURL,
			GitBranch:   proj.GitBranch,
			AutoDeploy:  proj.AutoDeploy,
			User:        proj.User,
		},
		Services: make([]models.ExportServiceInfo, 0, len(svcs)),
	}
	for _, svc := range svcs {
		out.Services = append(out.Services, models.ExportServiceInfo{
			Name:           svc.Name,
			Role:           svc.Role,
			Framework:      svc.Framework,
			GitSubpath:     svc.GitSubpath,
			GitBranch:      svc.GitBranch,
			PathPrefix:     svc.PathPrefix,
			PrimaryDomain:  svc.PrimaryDomain,
			AliasDomains:   append([]string(nil), svc.AliasDomains...),
			InstallCmd:     svc.InstallCmd,
			BuildCmd:       svc.BuildCmd,
			StartCmd:       svc.StartCmd,
			RuntimeVersion: svc.RuntimeVersion,
			Port:           svc.Port,
			EnvVars:        copyEnvVars(svc.EnvVars),
		})
	}
	return out, nil
}

// copyEnvVars returns a defensive shallow copy of an env_vars map. The
// service-side map shares its backing storage with the cached Project
// document; serialising the export shouldn't mutate it.
func copyEnvVars(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// Import re-hydrates a ProjectExport manifest into a fresh project on this
// panel by translating it to a ProvisionProjectRequest and delegating to
// Provision. This means the import inherits the same:
//
//   - Atomic rollback: if ANY service fails to install/build/start, the
//     partially-created project is fully torn down (services removed,
//     nginx vhosts deleted, on-disk code wiped, project row deleted) so
//     a retry doesn't hit a unique-slug collision on a stranded row.
//   - Slug allocation: name collisions retry with "<slug>-2", "<slug>-3",
//     ..., up to -50 — same as the New Project wizard.
//   - Webhook secret minting: a fresh per-project HMAC secret is
//     generated, so the imported project gets a NEW URL+secret pair.
//     The source's secret stays valid for the source project; importing
//     does NOT touch the source.
//   - Tenant scoping: scope is read from ctx via GetCallerScope, same as
//     Create — vendor_admin imports land under their tenant; vendor_owner
//     imports land under whoever owned the (optional) destination user.
//
// Validation runs BEFORE Provision so we fail fast on a missing repo URL
// or an unknown schema_version without leaving a half-created project.
//
// Domain uniqueness: the panel enforces globally-unique primary_domain at
// the service layer. When the import is into the same panel that produced
// the manifest, OverrideDomains MUST remap every original domain to a free
// one — otherwise the AddService call will reject the row and the whole
// import rolls back. The handler surfaces the per-service error so the
// operator can fix the override map and retry.
func (s *ProjectService) Import(ctx context.Context, req *models.ImportProjectRequest) (*ProvisionResult, error) {
	if req == nil {
		return nil, fmt.Errorf("import request is required")
	}
	m := req.Manifest
	if m.SchemaVersion == 0 {
		return nil, fmt.Errorf("manifest missing schema_version — not a valid project export")
	}
	if m.SchemaVersion > models.CurrentProjectExportSchemaVersion {
		return nil, fmt.Errorf("manifest schema_version %d is newer than this panel supports (%d) — upgrade the destination panel first", m.SchemaVersion, models.CurrentProjectExportSchemaVersion)
	}
	if strings.TrimSpace(m.Project.GitRepoURL) == "" {
		return nil, fmt.Errorf("manifest is missing project.git_repo_url")
	}
	if len(m.Services) == 0 {
		return nil, fmt.Errorf("manifest has zero services — at least one service is required to provision a project")
	}

	name := strings.TrimSpace(req.OverrideName)
	if name == "" {
		name = strings.TrimSpace(m.Project.Name)
	}
	if name == "" {
		return nil, fmt.Errorf("manifest is missing project.name")
	}

	user := strings.TrimSpace(req.User)
	if user == "" {
		user = strings.TrimSpace(m.Project.User)
	}

	branch := strings.TrimSpace(m.Project.GitBranch)
	if branch == "" {
		branch = "main"
	}

	services := make([]models.AddServiceRequest, 0, len(m.Services))
	for i, svc := range m.Services {
		domain := strings.TrimSpace(svc.PrimaryDomain)
		if req.OverrideDomains != nil {
			if remapped, ok := req.OverrideDomains[svc.PrimaryDomain]; ok && strings.TrimSpace(remapped) != "" {
				domain = strings.TrimSpace(remapped)
			}
		}
		if domain == "" {
			return nil, fmt.Errorf("service[%d] %q is missing primary_domain (and no override supplied)", i, svc.Name)
		}
		services = append(services, models.AddServiceRequest{
			Name:           svc.Name,
			Role:           svc.Role,
			Framework:      svc.Framework,
			GitRepoURL:     m.Project.GitRepoURL,
			GitSubpath:     svc.GitSubpath,
			GitBranch:      branch,
			PathPrefix:     svc.PathPrefix,
			PrimaryDomain:  domain,
			AliasDomains:   append([]string(nil), svc.AliasDomains...),
			InstallCmd:     svc.InstallCmd,
			BuildCmd:       svc.BuildCmd,
			StartCmd:       svc.StartCmd,
			RuntimeVersion: svc.RuntimeVersion,
			Port:           svc.Port,
			EnvVars:        copyEnvVars(svc.EnvVars),
			User:           user,
		})
	}

	provReq := &models.ProvisionProjectRequest{
		Name:        name,
		Description: m.Project.Description,
		GitHubPAT:   strings.TrimSpace(req.GitHubPAT),
		AutoDeploy:  m.Project.AutoDeploy,
		GitRepoURL:  strings.TrimSpace(m.Project.GitRepoURL),
		GitBranch:   branch,
		User:        user,
		Services:    services,
	}
	res, err := s.Provision(ctx, provReq)
	if err != nil {
		return nil, err
	}
	log.Info().
		Str("project_id", res.Project.ID.Hex()).
		Str("name", res.Project.Name).
		Int("services", len(res.Services)).
		Int("schema_version", m.SchemaVersion).
		Str("source_panel_version", m.PanelVersion).
		Msg("project imported from JSON manifest")
	return res, nil
}

// List returns every project the caller has access to, paged. Tenant-scoped
// users only see their own tenant's projects; vendor_owner sees everything
// (mirrors the App List policy and is what makes server-transfer-imported
// projects visible to the destination admin even when the source vendor's
// User row didn't survive the sync).
func (s *ProjectService) List(ctx context.Context, page, limit int) ([]models.Project, int64, error) {
	col := s.db.Collection(database.ColProjects)
	filter := bson.M{}
	if scope := GetCallerScope(ctx); scope != nil && constants.IsTenantScoped(scope.Role) && scope.TenantHex != "" {
		if tid, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
			filter["tenant_id"] = tid
		}
	}

	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	skip := int64((page - 1) * limit)
	cur, err := col.Find(ctx, filter, options.Find().SetSkip(skip).SetLimit(int64(limit)).SetSort(bson.D{{Key: "created_at", Value: -1}}))
	if err != nil {
		return nil, 0, err
	}
	defer cur.Close(ctx)

	var list []models.Project
	if err := cur.All(ctx, &list); err != nil {
		return nil, 0, err
	}
	for i := range list {
		list[i].GitHubPATEncrypted = nil
		list[i].WebhookSecret = ""
	}
	if list == nil {
		list = []models.Project{}
	}
	return list, total, nil
}

// Get returns a project by ID with its services attached via a side channel —
// the handler calls ListServices separately so the Project document itself
// stays small.
func (s *ProjectService) Get(ctx context.Context, id string) (*models.Project, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid project id")
	}
	var p models.Project
	if err := s.db.Collection(database.ColProjects).FindOne(ctx, bson.M{"_id": oid}).Decode(&p); err != nil {
		return nil, err
	}
	p.GitHubPATEncrypted = nil
	p.WebhookSecret = ""
	return &p, nil
}

// Update patches mutable project fields. Ignores nil pointers so PATCH-style
// partial updates work cleanly.
func (s *ProjectService) Update(ctx context.Context, id string, req *models.UpdateProjectRequest) (*models.Project, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid project id")
	}
	set := bson.M{"updated_at": time.Now()}
	if req.Name != nil {
		set["name"] = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		set["description"] = *req.Description
	}
	if req.AutoDeploy != nil {
		set["auto_deploy"] = *req.AutoDeploy
	}
	if req.Paused != nil {
		set["paused"] = *req.Paused
	}
	if req.GitRepoURL != nil {
		newURL := strings.TrimRight(strings.TrimSpace(*req.GitRepoURL), "/")
		if newURL != "" && !strings.HasPrefix(newURL, "https://") && !strings.HasPrefix(newURL, "git@") {
			return nil, fmt.Errorf("git_repo_url must be an https:// URL (or git@ SSH)")
		}
		set["git_repo_url"] = newURL
		// Mirror onto every service so legacy code paths that still
		// read svc.GitRepoURL stay consistent.
		s.db.Collection(database.ColProjectServices).UpdateMany(ctx, bson.M{"project_id": oid}, bson.M{
			"$set": bson.M{"git_repo_url": newURL, "updated_at": time.Now()},
		})
		// Also rewrite the on-disk origin so the next pull goes to the
		// new URL. Best-effort — the next inPlaceSync will set-url
		// again with the latest token if this misses.
		if proj, perr := s.loadProject(ctx, oid); perr == nil && proj.ProjectDir != "" {
			agent.RunCommand(ctx, "git", "-c", "safe.directory="+proj.ProjectDir, "-C", proj.ProjectDir, "remote", "set-url", "origin", newURL)
		}
	}
	// Project-level branch update (3.1.27). Propagate to every
	// service row so legacy reads of svc.GitBranch stay in sync. The
	// next Pull / runDeploy on the shared clone will check out the
	// new branch via inPlaceSync's git fetch + reset --hard.
	if req.GitBranch != nil {
		newBranch := strings.TrimSpace(*req.GitBranch)
		if newBranch == "" {
			newBranch = "main"
		}
		set["git_branch"] = newBranch
		s.db.Collection(database.ColProjectServices).UpdateMany(ctx, bson.M{"project_id": oid}, bson.M{
			"$set": bson.M{"git_branch": newBranch, "updated_at": time.Now()},
		})
	}
	if _, err := s.db.Collection(database.ColProjects).UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": set}); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete cascades: removes every service (systemd units, vhosts, on-disk
// code) and then the project document itself. Service-level cleanup is
// best-effort — one broken vhost shouldn't keep the rest from being deleted.
func (s *ProjectService) Delete(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid project id")
	}
	proj, _ := s.loadProject(ctx, oid)
	svcs, _ := s.listServicesForProject(ctx, oid)
	for _, svc := range svcs {
		_ = s.removeServiceInternal(ctx, &svc)
	}
	// In NEW project-level layout, the shared clone wasn't touched by
	// removeServiceInternal — soft-delete it now so the operator's code
	// + .git survive under <projectDir>.deleted-<ts> for recovery.
	if proj != nil && proj.ProjectDir != "" {
		softDeleteDir(proj.ProjectDir, "deleted")
	}
	_, _ = s.db.Collection(database.ColProjectServices).DeleteMany(ctx, bson.M{"project_id": oid})
	_, _ = s.db.Collection(database.ColProjectDeployments).DeleteMany(ctx, bson.M{"project_id": oid})
	_, err = s.db.Collection(database.ColProjects).DeleteOne(ctx, bson.M{"_id": oid})
	return err
}

// RegenerateWebhookSecret mints a fresh per-project HMAC secret, persists it,
// and returns the new plaintext value so the UI can copy it into the GitHub
// webhook config. Mirrors RotatePAT's contract: the OLD secret is overwritten
// in place, and any in-flight GitHub deliveries that were signed with the old
// secret will fail signature verification on the panel side — operator MUST
// paste the new secret into GitHub's webhook Secret field before the next
// push, or every delivery will be rejected with "signature mismatch" until
// they do.
//
// Use cases:
//   - The original secret leaked (committed to a repo, posted in a chat).
//   - Post-server-migration hygiene — the source's encrypted_pass blob is
//     gone but the webhook secret was carried verbatim; rotating to fresh
//     entropy under the destination's install is a reasonable defence-in-
//     depth move.
//   - The operator wants a clean rotation cycle on a recurring cadence.
//
// Returns the new plaintext secret (not stored — caller is responsible for
// surfacing it to the operator immediately).
func (s *ProjectService) RegenerateWebhookSecret(ctx context.Context, id string) (string, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return "", fmt.Errorf("invalid project id")
	}
	newSecret, err := generateWebhookSecret()
	if err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	res, err := s.db.Collection(database.ColProjects).UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{
			"webhook_secret": newSecret,
			"updated_at":     time.Now(),
		},
	})
	if err != nil {
		return "", err
	}
	if res.MatchedCount == 0 {
		return "", fmt.Errorf("project not found")
	}
	return newSecret, nil
}

// RotatePAT replaces the stored encrypted PAT with a new one. The old token is
// simply overwritten — callers who want audit history can record it via the
// AuditLogger middleware that's already wired up on these routes.
func (s *ProjectService) RotatePAT(ctx context.Context, id string, newPAT string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid project id")
	}
	newPAT = strings.TrimSpace(newPAT)
	if newPAT == "" {
		return fmt.Errorf("new PAT cannot be empty")
	}
	enc, err := crypto.EncryptGCM([]byte(newPAT), s.encKey)
	if err != nil {
		return err
	}
	_, err = s.db.Collection(database.ColProjects).UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{
			"github_pat_encrypted": enc,
			"github_pat_masked":    crypto.MaskToken(newPAT),
			"updated_at":           time.Now(),
		},
	})
	return err
}

// GetWebhookURL returns the public URL GitHub should POST to for this project.
// Built from the configured PublicWebhookBaseURL so dev and prod have
// different values without code changes.
func (s *ProjectService) GetWebhookURL(projectID string) string {
	return fmt.Sprintf("%s/api/v1/deploy/webhooks/project/%s", s.webhookBaseURL, projectID)
}

// GetWebhookSecret is intentionally separate from GetWebhookURL so the URL can
// be shown without revealing the secret; the UI renders secret with a reveal
// button next to a copy button.
//
// Auto-heal (3.1.73): legacy projects from before the per-project HMAC secret
// existed, plus projects migrated from a transfer payload that didn't carry
// the secret blob, would surface here as an empty string. The UI rendered
// "•" * 0 (i.e. nothing) and any GitHub delivery against them would fail
// signature verification silently because githubsig.VerifySignature returns
// false on empty secret. We now mint a fresh secret in place on the first
// GetWebhookSecret call when the field is empty — same shape as the one
// Create() generates — and persist it so subsequent calls return a stable
// value. The operator just needs to paste it into GitHub once.
func (s *ProjectService) GetWebhookSecret(ctx context.Context, id string) (string, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return "", fmt.Errorf("invalid project id")
	}
	var p models.Project
	if err := s.db.Collection(database.ColProjects).FindOne(ctx, bson.M{"_id": oid}).Decode(&p); err != nil {
		return "", err
	}
	if strings.TrimSpace(p.WebhookSecret) == "" {
		fresh, gerr := generateWebhookSecret()
		if gerr != nil {
			return "", fmt.Errorf("generate webhook secret: %w", gerr)
		}
		if _, uerr := s.db.Collection(database.ColProjects).UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
			"$set": bson.M{"webhook_secret": fresh, "updated_at": time.Now()},
		}); uerr != nil {
			return "", uerr
		}
		log.Info().Str("project_id", oid.Hex()).Msg("auto-healed missing webhook secret")
		p.WebhookSecret = fresh
	}
	return p.WebhookSecret, nil
}

// ReencryptPATForTransfer takes a GitHub PAT cipher sealed under the SOURCE's
// APP_ENCRYPTION_KEY and returns a fresh cipher sealed under THIS panel's
// key. Mirrors PanelMailService / WebhookService.ReencryptForTransfer so
// migrated Deploy Software projects come up with a working PAT instead of
// breaking auto-deploy and `git pull` until the operator manually rotates
// each one through the Project Settings page.
//
// Same error contract as the other Reencrypt helpers: empty input returns
// (nil, nil); decryption with a wrong source key returns (nil, error) so
// the caller can surface a "re-enter PAT" warning rather than stamping
// garbage into the destination.
func (s *ProjectService) ReencryptPATForTransfer(srcCipher []byte, srcEncKeyRaw string) ([]byte, error) {
	if len(srcCipher) == 0 || strings.TrimSpace(srcEncKeyRaw) == "" {
		return nil, nil
	}
	if len(s.encKey) != 32 {
		return nil, fmt.Errorf("destination encryption key unavailable")
	}
	srcKey, err := crypto.LoadKey(srcEncKeyRaw)
	if err != nil {
		return nil, fmt.Errorf("load source key: %w", err)
	}
	plain, err := crypto.DecryptGCM(srcCipher, srcKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt with source key: %w", err)
	}
	return crypto.EncryptGCM(plain, s.encKey)
}

// decryptPAT returns the plaintext GitHub PAT for a project, or empty string if
// none was ever set. Errors from decryption (bad ciphertext, wrong key) bubble
// up — callers treat them as fatal since git clone is about to fail anyway.
func (s *ProjectService) decryptPAT(p *models.Project) (string, error) {
	if len(p.GitHubPATEncrypted) == 0 {
		return "", nil
	}
	plain, err := crypto.DecryptGCM(p.GitHubPATEncrypted, s.encKey)
	if err != nil {
		return "", fmt.Errorf("decrypt PAT: %w", err)
	}
	return string(plain), nil
}

// loadProject returns the raw (including secret + encrypted PAT) Project by
// ID. Internal use only — handlers read through Get() which strips secrets.
//
// Heal-on-read: as of 3.1.27 the canonical git branch lives on the
// Project, not the per-service rows. Projects created before that
// point have an empty Project.GitBranch even though their services
// each carry one. On first read we backfill from the first service's
// branch and persist so every subsequent read (and every Pull /
// runDeploy that keys off project.GitBranch) sees a consistent value
// without an explicit migration step the operator has to run.
func (s *ProjectService) loadProject(ctx context.Context, oid primitive.ObjectID) (*models.Project, error) {
	var p models.Project
	if err := s.db.Collection(database.ColProjects).FindOne(ctx, bson.M{"_id": oid}).Decode(&p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.GitBranch) == "" {
		// Walk the project's services in deterministic order (by
		// _id, which encodes creation time) so the heal picks the
		// FIRST service's branch — the same one the operator
		// originally typed in the wizard. Fallback to "main" when
		// the project has no services yet (rare but possible during
		// a partial Provision rollback).
		var firstSvc struct {
			Branch string `bson:"git_branch"`
		}
		_ = s.db.Collection(database.ColProjectServices).
			FindOne(ctx,
				bson.M{"project_id": p.ID},
				options.FindOne().SetSort(bson.D{{Key: "_id", Value: 1}})).
			Decode(&firstSvc)
		branch := strings.TrimSpace(firstSvc.Branch)
		if branch == "" {
			branch = "main"
		}
		_, _ = s.db.Collection(database.ColProjects).UpdateOne(ctx,
			bson.M{"_id": p.ID},
			bson.M{"$set": bson.M{"git_branch": branch, "updated_at": time.Now()}})
		p.GitBranch = branch
	}
	return &p, nil
}

// assignProjectOwnership re-stamps a freshly-created project's
// tenant_id + owner_user_id from the OWNING vendor's user record
// instead of the admin who actually pressed Create. The User Panel
// projects list filters on tenant_id == caller_tenant, so without
// this re-stamp a WHM-admin-provisioned project would only ever
// appear in the admin's list and stay invisible to the vendor it
// was provisioned for. Mutates the in-memory `proj` too so the
// rest of Provision sees consistent values.
//
// projectUser is the linux username (e.g. "konsultkaro"). When the
// lookup misses (unknown user, or the synthetic sp-<slug>-<hash>
// fallback), the function silently no-ops and the original admin
// scope stays — same conservative shape as the rest of the
// project flow's missing-user handling.
func (s *ProjectService) assignProjectOwnership(ctx context.Context, proj *models.Project, projectUser string) {
	if proj == nil || strings.TrimSpace(projectUser) == "" {
		return
	}
	var u models.User
	err := s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"username": projectUser}).Decode(&u)
	if err != nil {
		return
	}
	// Resolve the tenant the way the rest of the panel does: for
	// vendor_owner / vendor_admin (tenant roots) it equals the user's
	// own _id; for staff / customer it points at the parent vendor.
	var tid primitive.ObjectID
	if hex := resolveTenantID(&u); hex != "" {
		if oid, perr := primitive.ObjectIDFromHex(hex); perr == nil {
			tid = oid
		}
	}
	set := bson.M{"owner_user_id": u.ID, "updated_at": time.Now()}
	if !tid.IsZero() {
		set["tenant_id"] = tid
	}
	if _, derr := s.db.Collection(database.ColProjects).UpdateOne(ctx, bson.M{"_id": proj.ID}, bson.M{"$set": set}); derr == nil {
		proj.OwnerUserID = u.ID
		if !tid.IsZero() {
			proj.TenantID = tid
		}
	}
}

// listServicesForProject fetches every service for a project, regardless of
// caller scope. Used internally (cascade delete, webhook fan-out). Handler
// path goes through ListServices which returns the same result.
func (s *ProjectService) listServicesForProject(ctx context.Context, projectID primitive.ObjectID) ([]models.ProjectService, error) {
	cur, err := s.db.Collection(database.ColProjectServices).Find(ctx, bson.M{"project_id": projectID})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var list []models.ProjectService
	if err := cur.All(ctx, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// ListServices is the handler-facing variant: same query, sorted so UI
// ordering is stable.
// ListAllServices returns every Deploy Software service the caller can
// see, flat across all projects, with the parent project's name + slug
// stamped on each row so the consumer can render context without a
// second round-trip.
//
// Tenant scoping: vendor_owner sees everything; tenant-scoped roles
// only see services whose project_id is in their tenant's project set.
// We intentionally re-derive the project list with the same filter
// the JWT-driven Projects page uses, then look up services with
// project_id IN (...). One Find on each collection — no aggregation —
// because the per-tenant project count is small (typically <100) and
// staying out of $lookup keeps the query plan cheap on shared mongo.
//
// `search` is a case-insensitive substring match against the service
// name OR primary domain so an operator can grep "shop" and find both
// "shop-api" and "shop.example.com" in one query. Empty search returns
// every service.
func (s *ProjectService) ListAllServices(ctx context.Context, page, limit int, search string) ([]ProjectServiceWithContext, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	// 1. Resolve the project set the caller can see. Reuse the same
	//    tenant filter the regular project list applies — drift here
	//    would let a vendor see services that belong to another tenant.
	projCol := s.db.Collection(database.ColProjects)
	projFilter := bson.M{}
	if scope := GetCallerScope(ctx); scope != nil && constants.IsTenantScoped(scope.Role) && scope.TenantHex != "" {
		if tid, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
			projFilter["tenant_id"] = tid
		}
	}
	projCur, err := projCol.Find(ctx, projFilter, options.Find().SetProjection(bson.M{
		"name": 1, "slug": 1, "user": 1,
	}))
	if err != nil {
		return nil, 0, err
	}
	defer projCur.Close(ctx)
	type projRow struct {
		ID   primitive.ObjectID `bson:"_id"`
		Name string             `bson:"name"`
		Slug string             `bson:"slug"`
		User string             `bson:"user"`
	}
	var projects []projRow
	if err := projCur.All(ctx, &projects); err != nil {
		return nil, 0, err
	}
	if len(projects) == 0 {
		return []ProjectServiceWithContext{}, 0, nil
	}
	projIDs := make([]primitive.ObjectID, 0, len(projects))
	projMeta := make(map[primitive.ObjectID]projRow, len(projects))
	for _, p := range projects {
		projIDs = append(projIDs, p.ID)
		projMeta[p.ID] = p
	}

	// 2. Build the service-side filter. project_id IN scope set, plus
	//    optional search. Search uses regex on `name` OR `primary_domain`
	//    — escaped via QuoteMeta so a stray `.` from a domain match
	//    can't widen the search.
	svcFilter := bson.M{"project_id": bson.M{"$in": projIDs}}
	if q := strings.TrimSpace(search); q != "" {
		safe := regexp.QuoteMeta(q)
		svcFilter["$or"] = bson.A{
			bson.M{"name": bson.M{"$regex": safe, "$options": "i"}},
			bson.M{"primary_domain": bson.M{"$regex": safe, "$options": "i"}},
		}
	}

	svcCol := s.db.Collection(database.ColProjectServices)
	total, err := svcCol.CountDocuments(ctx, svcFilter)
	if err != nil {
		return nil, 0, err
	}
	skip := int64((page - 1) * limit)
	cur, err := svcCol.Find(ctx, svcFilter,
		options.Find().
			SetSort(bson.D{{Key: "created_at", Value: -1}}).
			SetSkip(skip).
			SetLimit(int64(limit)),
	)
	if err != nil {
		return nil, 0, err
	}
	defer cur.Close(ctx)
	var rows []models.ProjectService
	if err := cur.All(ctx, &rows); err != nil {
		return nil, 0, err
	}

	// 3. Stamp parent context onto each service. Done client-side so
	//    we don't need a $lookup; the project map fits comfortably in
	//    memory at any realistic tenant size.
	out := make([]ProjectServiceWithContext, 0, len(rows))
	for _, r := range rows {
		meta := projMeta[r.ProjectID]
		r.AttachedDomains = attachedDomainNames(ctx, s.db, r.ID)
		out = append(out, ProjectServiceWithContext{
			ProjectService: r,
			ProjectName:    meta.Name,
			ProjectSlug:    meta.Slug,
			ProjectUser:    meta.User,
		})
	}
	return out, total, nil
}

// ProjectServiceWithContext carries the project's display fields
// alongside the service so the consumer can render "shop-api · in
// project Storefront (storefront)" without a second API call. Embeds
// ProjectService so existing JSON consumers see every original
// field at the top level — the new project_* keys are additive.
type ProjectServiceWithContext struct {
	models.ProjectService `bson:",inline"`
	ProjectName           string `json:"project_name" bson:"project_name"`
	ProjectSlug           string `json:"project_slug" bson:"project_slug"`
	ProjectUser           string `json:"project_user" bson:"project_user"`
}

func (s *ProjectService) ListServices(ctx context.Context, projectID string) ([]models.ProjectService, error) {
	oid, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		return nil, fmt.Errorf("invalid project id")
	}
	cur, err := s.db.Collection(database.ColProjectServices).Find(ctx, bson.M{"project_id": oid}, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var list []models.ProjectService
	if err := cur.All(ctx, &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []models.ProjectService{}
	}
	for i := range list {
		list[i].AttachedDomains = attachedDomainNames(ctx, s.db, list[i].ID)
	}
	return list, nil
}

// GetService returns a single service by ID.
func (s *ProjectService) GetService(ctx context.Context, svcID string) (*models.ProjectService, error) {
	oid, err := primitive.ObjectIDFromHex(svcID)
	if err != nil {
		return nil, fmt.Errorf("invalid service id")
	}
	var svc models.ProjectService
	if err := s.db.Collection(database.ColProjectServices).FindOne(ctx, bson.M{"_id": oid}).Decode(&svc); err != nil {
		return nil, err
	}
	svc.AttachedDomains = attachedDomainNames(ctx, s.db, svc.ID)
	return &svc, nil
}

// AddService clones the repo, builds, starts, and wires up nginx + SSL for a
// new service under an existing project. Returns the service record with
// final computed fields (port, install dir, systemd unit name) populated.
func (s *ProjectService) AddService(ctx context.Context, projectID string, req *models.AddServiceRequest) (*models.ProjectService, error) {
	poid, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		return nil, fmt.Errorf("invalid project id")
	}
	proj, err := s.loadProject(ctx, poid)
	if err != nil {
		return nil, err
	}

	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	req.PrimaryDomain = sanitizeDomain(req.PrimaryDomain)
	for i, a := range req.AliasDomains {
		req.AliasDomains[i] = sanitizeDomain(a)
	}
	req.GitRepoURL = strings.TrimRight(strings.TrimSpace(req.GitRepoURL), "/")
	// Inherit the project-level repo URL when the request omits one —
	// every service in a project shares the same clone, so making the
	// caller resend the URL on each Add Service call was both
	// redundant and bug-prone (the frontend used to derive it from
	// the first existing service, which broke for projects with zero
	// services and rendered "(no repo set)" in the picker).
	if req.GitRepoURL == "" {
		req.GitRepoURL = strings.TrimRight(strings.TrimSpace(proj.GitRepoURL), "/")
	}
	// Inherit the project-level branch when the request omits one
	// (3.1.27 hoist — services don't have their own branch any
	// more, but legacy API callers may still send git_branch on
	// AddService payloads). Project.GitBranch always populated
	// thanks to loadProject's heal-on-read.
	if strings.TrimSpace(req.GitBranch) == "" {
		req.GitBranch = strings.TrimSpace(proj.GitBranch)
	}
	if strings.TrimSpace(req.GitBranch) == "" {
		req.GitBranch = "main"
	}
	if req.GitRepoURL == "" {
		// Both empty — legacy project that never had a project URL AND
		// caller didn't supply one. Surface this clearly instead of
		// letting the clone fail later with a less obvious message.
		return nil, fmt.Errorf("git_repo_url is required (project has none set; supply one in the Edit Project modal first)")
	}
	if err := validateServiceName(req.Name); err != nil {
		return nil, err
	}
	// Path-traversal guard: a malicious GitSubpath like "../../etc" would
	// otherwise let the subpath-move step escape the cloned tempdir.
	cleanSubpath, err := validateGitSubpath(req.GitSubpath)
	if err != nil {
		return nil, err
	}
	req.GitSubpath = cleanSubpath
	// Env var keys get written to a .env file and passed as systemd
	// Environment= lines; characters outside POSIX env-name syntax
	// are a shell-injection risk.
	if err := validateEnvVars(req.EnvVars); err != nil {
		return nil, err
	}
	if req.GitBranch == "" {
		req.GitBranch = "main"
	}
	if err := validateBranch(req.GitBranch); err != nil {
		return nil, err
	}
	// --- Tenant-domain guard --------------------------------------------
	//
	// Every domain the new service touches (primary + every alias) MUST be
	// a panel-registered domain owned by the project's linked vendor.
	//
	// The WHM Add Service modal already enforces this client-side by
	// filtering the domain dropdown to `d.user === project.user` (see
	// DeploySoftwarePage's AddServiceModal availableDomains prop). But
	// the bulk-upload path and any direct API caller bypass that UI
	// filter — pre-3.1.58 a doctored CSV could attach a service to a
	// domain that lives under /home/<other-vendor>/, breaking SSL
	// issuance (certbot can't write to a different vendor's home dir)
	// and silently bleeding nginx vhosts across tenant boundaries.
	//
	// We enforce here at the service layer so EVERY caller — UI form,
	// bulk upload, programmatic /external API — gets the same guarantee
	// without each entrypoint re-implementing the check. The actual
	// rule lives in the pure helper assertProjectDomainOwnership so a
	// unit test can exercise the matrix (mismatch, unregistered, alias
	// mismatch, etc.) without spinning up Mongo.
	if err := assertProjectDomainOwnership(proj, req, func(d string) string {
		return s.lookupDomainOwner(ctx, d)
	}); err != nil {
		return nil, err
	}

	if req.User == "" {
		// Legacy fallback path: project has no linked vendor (pre-3.1.27
		// hoist) AND assertProjectDomainOwnership returned nil (which it
		// does for proj.User == ""). Resolve from the primary domain's
		// owner; fall back to a synthetic sp-<slug>-* account when the
		// domain isn't registered yet. New projects always set proj.User
		// at Provision time so this branch only runs for legacy projects.
		if owner := s.lookupDomainOwner(ctx, req.PrimaryDomain); owner != "" {
			req.User = owner
		} else {
			req.User = defaultProjectUser(proj.Slug)
		}
	}

	// Apply framework preset defaults.
	if req.Framework != "" {
		if p, ok := lookupPreset(req.Framework); ok {
			if req.InstallCmd == "" {
				req.InstallCmd = p.InstallCmd
			}
			if req.BuildCmd == "" {
				req.BuildCmd = p.BuildCmd
			}
			if req.StartCmd == "" {
				req.StartCmd = p.StartCmd
			}
			if req.Port == 0 && p.DefaultPort > 0 {
				req.Port = p.DefaultPort
			}
		}
	}

	// Duplicate service name within project is a unique-index violation; catch
	// early for a better error message.
	if err := s.db.Collection(database.ColProjectServices).FindOne(ctx, bson.M{"project_id": poid, "name": req.Name}).Err(); err == nil {
		return nil, fmt.Errorf("service %q already exists in this project", req.Name)
	}

	if err := ensureUser(ctx, req.User); err != nil {
		return nil, err
	}

	// Two layouts:
	//   useProjectClone=true  (NEW)   — proj.ProjectDir holds ONE clone for
	//     the whole project. Each service's install_dir is a subdirectory
	//     of that clone, named after its GitSubpath. The shared clone means
	//     `git pull` updates every service in one operation, and disk usage
	//     stays linear in repo size instead of N copies.
	//   useProjectClone=false (LEGACY) — every service has its own clone at
	//     /home/<user>/projects/<slug>/<svc-name>/. Kept for backward
	//     compatibility with projects created before the project-level
	//     refactor.
	useProjectClone := proj.ProjectDir != "" && proj.GitRepoURL != ""
	var installDir string
	if useProjectClone {
		// install_dir IS the subpath dir inside the project's clone, so app
		// commands run there directly with no double-nesting. When no
		// subpath is set, install_dir = projectDir itself (the repo root
		// IS the app — common single-app project layout). Two no-subpath
		// services in the same project would collide here; the
		// uniqueness check below catches that and refuses with a clear
		// error, sparing operators a confusing "subpath does not exist"
		// failure later.
		if cleanSubpath != "" {
			installDir = filepath.Join(proj.ProjectDir, cleanSubpath)
			// Reject duplicate subpath in the same project — both
			// services would resolve to the same install_dir and fight
			// over .env, node_modules, and the systemd unit. Operators
			// should pick a different subpath or share via a single
			// service with multiple aliases.
			if r := s.db.Collection(database.ColProjectServices).FindOne(ctx, bson.M{
				"project_id":  poid,
				"git_subpath": cleanSubpath,
			}); r != nil && r.Err() == nil {
				return nil, fmt.Errorf("another service in this project already uses subpath %q; pick a different subdirectory", cleanSubpath)
			}
		} else {
			installDir = proj.ProjectDir
			// Reject duplicate empty subpath in the same project — both
			// services would resolve to projectDir and fight over .env /
			// node_modules / systemd unit. Suggest setting a subpath.
			if r := s.db.Collection(database.ColProjectServices).FindOne(ctx, bson.M{
				"project_id":  poid,
				"git_subpath": "",
			}); r != nil && r.Err() == nil {
				return nil, fmt.Errorf("another service in this project already uses the repo root (no subpath); set a git_subpath to point this service at a subdirectory")
			}
		}
	} else {
		installDir = fmt.Sprintf("/home/%s/projects/%s/%s", req.User, proj.Slug, req.Name)
		if err := prepareAppDir(ctx, installDir, req.User); err != nil {
			return nil, err
		}
	}
	// Track whether we succeeded — if anything below this point fails
	// (clone, install, build, systemd, vhost, SSL, DB insert), the deferred
	// cleanup SOFT-DELETES the just-created installDir by renaming it to
	// installDir.failed-<ts>, NOT wiping it.
	//
	// Why soft-delete instead of rm -rf: the operator's repo is now sitting
	// at the failed dir and they may want to inspect what went wrong (e.g.
	// the partially-built `.next/` for a Next.js failure, the stderr log
	// the systemd unit left behind). Hard-deleting was the cause of the
	// "my projects/ folder is empty after every failed deploy" report.
	// On the next retry, prepareAppDir wipes whatever is at the active
	// path, so leaving stale dirs around doesn't break anything — and
	// File Manager exposes them for the operator to clean up by hand.
	// The .src tempdir is genuinely transient (pre-stage clone target)
	// so we still rm it.
	addSucceeded := false
	defer func() {
		if addSucceeded {
			return
		}
		softDeleteDir(installDir, "failed")
		agent.RunCommand(context.Background(), "rm", "-rf", installDir+".src")
	}()

	// --- Clone code into installDir --------------------------------------
	token, err := s.decryptPAT(proj)
	if err != nil {
		return nil, err
	}
	var workDir string
	if useProjectClone {
		// New layout: project's shared clone is already on disk at
		// proj.ProjectDir (placed there by Provision). install_dir is
		// already a subdirectory of that clone — just verify it exists.
		// .git lives at proj.ProjectDir, NOT at install_dir; git
		// operations target the project root in runDeploy / pull.
		if _, err := agent.RunCommand(ctx, "test", "-d", installDir); err != nil {
			return nil, fmt.Errorf("subpath %q does not exist in project repo", req.GitSubpath)
		}
		workDir = installDir
	} else {
		// Legacy per-service clone.
		tmp := installDir + ".src"
		agent.RunCommand(ctx, "rm", "-rf", tmp)
		// Hard timeout on the clone so a hung network doesn't block the worker
		// for the full 15-minute deploy context. 5 minutes is generous even for
		// large monorepos on slow connections.
		cloneCtx, cloneCancel := context.WithTimeout(ctx, 5*time.Minute)
		cloneErr := agent.GitClone(cloneCtx, req.GitRepoURL, req.GitBranch, tmp, token)
		cloneCancel()
		if cloneErr != nil {
			// NEVER return the raw error if it contains the injected token —
			// agent error messages sometimes echo the command line back.
			return nil, fmt.Errorf("git clone failed: %s", sanitiseGitError(cloneErr, token))
		}
		// Move the FULL clone (including .git) into installDir.
		if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("shopt -s dotglob && mv %s/* %s/ && rm -rf %s", tmp, installDir, tmp)); err != nil {
			return nil, fmt.Errorf("stage checkout: %w", err)
		}
		// Subpath validation in legacy layout — workDir is install_dir + subpath
		workDir = serviceWorkDir(installDir, req.GitSubpath)
		if workDir != installDir {
			if _, err := agent.RunCommand(ctx, "test", "-d", workDir); err != nil {
				return nil, fmt.Errorf("git_subpath %q does not exist in repo", req.GitSubpath)
			}
		}
		if err := chownRecursive(ctx, installDir, req.User); err != nil {
			return nil, err
		}
	}

	// --- Pre-flight: required env vars ------------------------------------
	// Many real-world repos ship a .env.example that documents the env
	// vars the app needs to even boot. If the operator left them blank,
	// we do NOT fail the deploy — the install/build/vhost/SSL all still
	// run successfully. We just refuse to START the service (it would
	// crash-loop on the missing vars) and flag it with status
	// "needs_env_vars" + the missing key list, so the WHM UI can show a
	// banner and let the operator fill them in via the Edit modal. Once
	// they do, the next start/restart picks up cleanly.
	missingEnvKeys := requiredEnvKeysFromExample(workDir, req.EnvVars)

	// --- Auto-detect from package.json -----------------------------------
	// Reads framework, port, install/build/start from the just-cloned source
	// and fills any fields the operator left blank. Everything the operator
	// did provide wins — detection never overrides explicit values.
	//
	// After detection, re-apply the matching framework preset to pick up
	// anything the package.json didn't specify (e.g. a Next.js repo with
	// only scripts.build — we still want the preset's start command).
	hints := DetectPackageJSONHints(workDir)
	if summary := applyPkgHints(&hints, &req.Framework, &req.InstallCmd, &req.BuildCmd, &req.StartCmd, &req.Port); summary != "" {
		fmt.Fprintf(os.Stderr, "[project %s/%s] %s\n", proj.Slug, req.Name, summary)
	}
	if req.Framework != "" {
		if p, ok := lookupPreset(req.Framework); ok {
			if req.InstallCmd == "" {
				req.InstallCmd = p.InstallCmd
			}
			if req.BuildCmd == "" {
				req.BuildCmd = p.BuildCmd
			}
			if req.StartCmd == "" {
				req.StartCmd = p.StartCmd
			}
			if req.Port == 0 && p.DefaultPort > 0 {
				req.Port = p.DefaultPort
			}
			// A detected react-vite / CRA project should also re-tag itself
			// as a static role so the deploy flow skips the systemd unit.
			if p.IsStatic && req.Role == "backend" {
				req.Role = "frontend"
			}
		}
	}

	// --- Write .env + build ----------------------------------------------
	if len(req.EnvVars) > 0 {
		var lines []string
		for k, v := range req.EnvVars {
			lines = append(lines, fmt.Sprintf("%s=%s", k, v))
		}
		writeFileAsUser(ctx, filepath.Join(workDir, ".env"), strings.Join(lines, "\n")+"\n", req.User, "0600")
	}

	runtimeBinDir := resolveRuntimeBinDir(resolveServiceAppType(req.Framework, req.Role), req.RuntimeVersion)
	if req.InstallCmd != "" {
		if err := runBuildAsUser(ctx, req.User, workDir, withNoColor(req.InstallCmd), runtimeBinDir); err != nil {
			return nil, buildErrorFrom("install", err)
		}
	}
	if req.BuildCmd != "" {
		if err := runBuildAsUser(ctx, req.User, workDir, withNoColor(req.BuildCmd), runtimeBinDir); err != nil {
			return nil, buildErrorFrom("build", err)
		}
	}

	// --- Port allocation for backend services ----------------------------
	if req.Role == "backend" {
		used := collectUsedPorts(ctx, s.db)
		if req.Port == 0 || used[req.Port] || !isPortFree(req.Port) {
			p, err := allocatePort(used)
			if err != nil {
				return nil, fmt.Errorf("allocate port: %w", err)
			}
			req.Port = p
		}
	}

	// --- Start systemd unit for backends ---------------------------------
	unitName := fmt.Sprintf("sp-proj-%s-%s", proj.Slug, req.Name)
	if req.Role == "backend" {
		env := map[string]string{}
		for k, v := range req.EnvVars {
			env[k] = v
		}
		env["PORT"] = fmt.Sprintf("%d", req.Port)
		// Mirror the pinned runtime into the systemd unit's PATH so the
		// running process uses the same interpreter version the build ran
		// under. Without this, a Next.js app built with Node 20 would end
		// up booted under whatever /usr/local/bin/node resolves to at
		// start time, and drift the moment a new `n` install lands.
		if runtimeBinDir != "" {
			env["PATH"] = runtimeBinDir + ":/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
		}
		startCmd := renderStartCmd(req.StartCmd, req.Port)
		if strings.TrimSpace(startCmd) == "" {
			return nil, fmt.Errorf("start_cmd is required for backend services")
		}
		// Pre-flight: if the start command is a direct `node X.js`
		// invocation, make sure X.js actually exists in the cloned repo.
		// Otherwise systemd will crash-loop the unit forever and all
		// nginx ever sees is a dead upstream (= 502 Bad Gateway).
		if entry := extractNodeEntry(startCmd); entry != "" {
			entryPath := filepath.Join(workDir, entry)
			if _, statErr := os.Stat(entryPath); os.IsNotExist(statErr) {
				// Prefer the repo's own scripts.start if it pointed at a
				// different (and presumably correct) entry file. If that
				// also can't help, fail clean with a specific message.
				if hints.StartCmd != "" && hints.StartCmd != req.StartCmd {
					if hintEntry := extractNodeEntry(hints.StartCmd); hintEntry != "" {
						if _, e2 := os.Stat(filepath.Join(workDir, hintEntry)); e2 == nil {
							req.StartCmd = hints.StartCmd
							startCmd = renderStartCmd(req.StartCmd, req.Port)
							fmt.Fprintf(os.Stderr, "[project %s/%s] start_cmd switched to package.json scripts.start (%s was missing)\n", proj.Slug, req.Name, entry)
						}
					}
				}
				// Re-check in case the fallback above fixed it.
				if entry := extractNodeEntry(startCmd); entry != "" {
					if _, e := os.Stat(filepath.Join(workDir, entry)); os.IsNotExist(e) {
						return nil, &BuildError{
							Stage:   "start",
							Summary: fmt.Sprintf("start command references %q but that file isn't in the repo", entry),
							Details: fmt.Sprintf("Your start command is:\n  %s\n\nBut %s doesn't exist at the work dir (%s).\n\nFix one of:\n  1. Change start_cmd to point at your real entry file (e.g. node index.js / node dist/server.js)\n  2. Add a scripts.start to package.json that runs the correct entry\n  3. Update GitSubpath to the directory that contains the entry file", startCmd, entry, workDir),
						}
					}
				}
			}
		}
		if err := agent.CreateSystemdUnit(ctx, unitName, req.User, workDir, startCmd, env); err != nil {
			return nil, fmt.Errorf("create systemd unit: %w", err)
		}
		// If the operator left required env vars unset, stop here BEFORE
		// trying to detect the port — the service won't actually start
		// (it would crash-loop on the missing vars), so waiting 20s for
		// a port that will never appear is just a bad UX. Stop the unit
		// so systemd doesn't keep crash-restarting in the background;
		// vhost still gets reconciled below so the domain isn't
		// dangling. The persist step writes status="needs_env_vars" +
		// the missing keys list so the WHM UI shows a banner.
		if len(missingEnvKeys) > 0 {
			agent.RunCommand(context.Background(), "systemctl", "stop", unitName)
			fmt.Fprintf(os.Stderr, "[project %s/%s] skipping start — %d env var(s) missing: %v\n", proj.Slug, req.Name, len(missingEnvKeys), missingEnvKeys)
		} else if detected := detectListeningPort(ctx, unitName, 20*time.Second, req.Port); detected > 0 {
			req.Port = detected
		} else {
			// 20 seconds passed and nothing in the unit's cgroup is
			// listening. Something went wrong between exec-ing the start
			// command and the first listen() call — the most useful
			// signal is the systemd journal tail, which has the actual
			// node/python/go error. Surfacing it as a typed BuildError
			// makes the frontend's BuildErrorModal pop with the real
			// reason instead of the operator watching a silent 502.
			journal := fetchUnitJournal(ctx, unitName, 40)
			summary := summariseBuildOutput(journal)
			if summary == "" {
				summary = "backend service didn't bind any port within 20s"
			}
			// Stop the crash-looping unit so it doesn't pollute logs
			// forever (removeServiceInternal via the defer cleanup will
			// also run, but this keeps the unit from restarting during
			// rollback).
			agent.RunCommand(context.Background(), "systemctl", "stop", unitName)
			agent.DeleteSystemdUnit(context.Background(), unitName)
			return nil, &BuildError{
				Stage:   "start",
				Summary: summary,
				Details: journal,
			}
		}
	}

	// --- Compute vhost + issue SSL ---------------------------------------
	buildDir := workDir
	if req.Framework != "" {
		if p, ok := lookupPreset(req.Framework); ok && p.StaticDir != "" {
			buildDir = filepath.Join(workDir, p.StaticDir)
		}
	}
	if err := s.reconcileVhostFor(ctx, proj, req.Role, req.PrimaryDomain, req.AliasDomains, req.PathPrefix, req.Port, buildDir); err != nil {
		return nil, fmt.Errorf("nginx/SSL: %w", err)
	}

	// --- Persist ---------------------------------------------------------
	now := time.Now()
	// "needs_env_vars" replaces "running" when the operator left
	// .env.example keys unfilled. The systemd unit + nginx vhost still
	// exist; once env vars are added the operator hits Start (or saves
	// the env editor with restart=true) and the service comes up
	// cleanly. Static services skip systemd entirely so this only
	// applies to backend/frontend roles that have a SystemdUnit.
	status := "running"
	if len(missingEnvKeys) > 0 && unitName != "" {
		status = "needs_env_vars"
	}
	svc := models.ProjectService{
		ProjectID:     poid,
		Name:          req.Name,
		Role:          req.Role,
		Framework:     req.Framework,
		GitRepoURL:    req.GitRepoURL,
		GitSubpath:    req.GitSubpath,
		GitBranch:     req.GitBranch,
		PathPrefix:    req.PathPrefix,
		PrimaryDomain: req.PrimaryDomain,
		AliasDomains:  req.AliasDomains,
		InstallCmd:     req.InstallCmd,
		BuildCmd:       req.BuildCmd,
		StartCmd:       req.StartCmd,
		RuntimeVersion: req.RuntimeVersion,
		Port:           req.Port,
		EnvVars:       req.EnvVars,
		User:          req.User,
		InstallDir:    installDir,
		BuildDir:      buildDir,
		SystemdUnit:   unitName,
		Status:        status,
		MissingEnvKeys: missingEnvKeys,
		LastDeployedAt: &now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	res, err := s.db.Collection(database.ColProjectServices).InsertOne(ctx, svc)
	if err != nil {
		return nil, err
	}
	svc.ID = res.InsertedID.(primitive.ObjectID)
	addSucceeded = true
	return &svc, nil
}

// UpdateService applies a partial patch and, if anything relevant to the
// running process changed, restarts the systemd unit and rewrites the vhost.
func (s *ProjectService) UpdateService(ctx context.Context, svcID string, req *models.UpdateServiceRequest) (*models.ProjectService, error) {
	svc, err := s.GetService(ctx, svcID)
	if err != nil {
		return nil, err
	}
	set := bson.M{"updated_at": time.Now()}
	needsRestart := false
	// Domain mutations are computed up front so we can reject conflicts
	// (alias == primary, dup aliases) before any DB writes happen, and
	// know whether a vhost reconcile + path_prefix rebuild is required.
	oldPrimary := svc.PrimaryDomain
	newPrimary := oldPrimary
	primaryChanged := false
	if req.PrimaryDomain != nil {
		p := sanitizeDomain(*req.PrimaryDomain)
		if p == "" {
			return nil, fmt.Errorf("primary_domain cannot be empty")
		}
		if p != oldPrimary {
			newPrimary = p
			primaryChanged = true
			set["primary_domain"] = p
		}
	}
	pathPrefix := svc.PathPrefix
	if req.PathPrefix != nil {
		pathPrefix = *req.PathPrefix
	}
	port := svc.Port
	if req.Port != nil {
		port = *req.Port
	}
	buildDir := svc.BuildDir
	// Resolve the alias list we want to end up with. nil = leave alone;
	// non-nil (incl. empty slice) = replace. Validation: trim+lowercase,
	// drop blanks/dupes, reject any equal to the (possibly new) primary.
	aliases := append([]string(nil), svc.AliasDomains...)
	aliasesChanged := false
	if req.AliasDomains != nil {
		seen := map[string]bool{}
		next := make([]string, 0, len(*req.AliasDomains))
		for _, raw := range *req.AliasDomains {
			a := sanitizeDomain(raw)
			if a == "" || seen[a] {
				continue
			}
			if a == newPrimary {
				return nil, fmt.Errorf("%s is already the primary domain", a)
			}
			seen[a] = true
			next = append(next, a)
		}
		aliases = next
		aliasesChanged = true
		set["alias_domains"] = next
	} else if primaryChanged {
		// Defensive: if the new primary collides with an existing alias,
		// silently drop that alias from the resolved list rather than
		// emitting a server_name with the same name twice (nginx warns
		// + ignores the dup, but the operator's intent is clearly "this
		// is now the primary, not also an alias").
		filtered := aliases[:0]
		for _, a := range aliases {
			if a != newPrimary {
				filtered = append(filtered, a)
			}
		}
		if len(filtered) != len(aliases) {
			aliases = filtered
			aliasesChanged = true
			set["alias_domains"] = aliases
		}
	}
	if req.Framework != nil {
		set["framework"] = *req.Framework
	}
	if req.GitBranch != nil {
		set["git_branch"] = *req.GitBranch
	}
	if req.GitSubpath != nil {
		set["git_subpath"] = *req.GitSubpath
	}
	if req.PathPrefix != nil {
		set["path_prefix"] = *req.PathPrefix
	}
	if req.InstallCmd != nil {
		set["install_cmd"] = *req.InstallCmd
	}
	if req.BuildCmd != nil {
		set["build_cmd"] = *req.BuildCmd
	}
	if req.StartCmd != nil {
		set["start_cmd"] = *req.StartCmd
		needsRestart = true
	}
	// Track runtime_version changes on backends: we have to rewrite the
	// systemd unit's Environment=PATH= so the running process switches to
	// the newly-picked interpreter. Build-time PATH auto-follows on the
	// next deploy because runDeploy reads runtime_version fresh.
	runtimeChanged := false
	if req.RuntimeVersion != nil && *req.RuntimeVersion != svc.RuntimeVersion {
		set["runtime_version"] = *req.RuntimeVersion
		runtimeChanged = true
	}
	if req.Port != nil {
		set["port"] = *req.Port
		needsRestart = true
	}
	if req.EnvVars != nil {
		set["env_vars"] = *req.EnvVars
		needsRestart = true
		// Re-evaluate the env-vars warning every time the operator
		// edits env_vars. If they've now supplied every key declared
		// in .env.example, clear the missing list AND the
		// "needs_env_vars" status so the next restart can flip the
		// service to "running" cleanly.
		if svc.InstallDir != "" {
			stillMissing := requiredEnvKeysFromExample(svc.InstallDir, *req.EnvVars)
			set["missing_env_keys"] = stillMissing
			if len(stillMissing) == 0 && svc.Status == "needs_env_vars" {
				set["status"] = "running"
			} else if len(stillMissing) > 0 && svc.Role == "backend" {
				set["status"] = "needs_env_vars"
			}
		}
	}
	if _, err := s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": svc.ID}, bson.M{"$set": set}); err != nil {
		return nil, err
	}
	// Persist the edited env to the on-disk .env so the running process
	// (and any tooling that reads .env) sees the change. Without this an
	// env-only edit only touched Mongo and never reached the box, so the
	// service kept running with stale values until runtime_version changed.
	// Mirrors AddService's .env write (above) and AppService.Update.
	if req.EnvVars != nil && svc.InstallDir != "" {
		wd := svc.InstallDir
		if proj, _ := s.loadProject(ctx, svc.ProjectID); proj == nil || proj.ProjectDir == "" {
			wd = serviceWorkDir(svc.InstallDir, svc.GitSubpath)
		}
		var lines []string
		for k, v := range *req.EnvVars {
			lines = append(lines, fmt.Sprintf("%s=%s", k, v))
		}
		writeFileAsUser(ctx, filepath.Join(wd, ".env"), strings.Join(lines, "\n")+"\n", svc.User, "0600")
	}
	// Vhost reconcile when domains or routing-relevant fields shifted.
	// reconcileVhostFor is idempotent and reads its inputs from the DB
	// row (via buildMergedVhostSpec), so by this point the new
	// primary_domain / alias_domains are already persisted and the
	// rebuild lands the correct server_name. On a primary RENAME we
	// must also unlink the OLD vhost file — without this the box would
	// answer for both names and nginx would log a "conflicting server
	// name" warning the next reload after any unrelated edit.
	if primaryChanged {
		agent.DeleteVhost(ctx, oldPrimary)
	}
	if primaryChanged || aliasesChanged || req.PathPrefix != nil {
		proj, perr := s.loadProject(ctx, svc.ProjectID)
		if perr == nil && newPrimary != "" {
			if err := s.reconcileVhostFor(ctx, proj, svc.Role, newPrimary, aliases, pathPrefix, port, buildDir); err != nil {
				return nil, fmt.Errorf("vhost reconcile after domain change: %w", err)
			}
		}
	}
	// Rewriting the unit covers runtime swaps: CreateSystemdUnit overwrites
	// the file in /etc/systemd/system, then daemon-reloads + restarts, so
	// the new Environment=PATH= takes effect in one go and `needsRestart`
	// becomes redundant for this call.
	if (runtimeChanged || req.EnvVars != nil) && svc.Role == "backend" && svc.SystemdUnit != "" {
		updated, _ := s.GetService(ctx, svcID)
		if updated != nil {
			proj, _ := s.loadProject(ctx, svc.ProjectID)
			workDir := svc.InstallDir
			if proj == nil || proj.ProjectDir == "" {
				workDir = serviceWorkDir(svc.InstallDir, svc.GitSubpath)
			}
			startCmd := renderStartCmd(updated.StartCmd, updated.Port)
			env := map[string]string{}
			for k, v := range updated.EnvVars {
				env[k] = v
			}
			if updated.Port > 0 {
				env["PORT"] = fmt.Sprintf("%d", updated.Port)
			}
			if rbd := resolveRuntimeBinDir(resolveServiceAppType(updated.Framework, updated.Role), updated.RuntimeVersion); rbd != "" {
				env["PATH"] = rbd + ":/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
			}
			_ = agent.CreateSystemdUnit(ctx, svc.SystemdUnit, updated.User, workDir, startCmd, env)
		}
	} else if needsRestart && svc.Role == "backend" {
		agent.RunCommand(ctx, "systemctl", "restart", svc.SystemdUnit)
	}
	// If the upstream port moved, every attached domain's standalone
	// reverse-proxy vhost is now pointing at a dead port — refresh each
	// one's cached proxy_port and rebuild its vhost so the domains follow
	// the service. No-op when this service has no attached domains.
	if req.Port != nil && *req.Port != svc.Port {
		if updated, _ := s.GetService(ctx, svcID); updated != nil {
			reapplyAttachedDomainsForService(ctx, s.db, updated)
		}
	}
	return s.GetService(ctx, svcID)
}

// RemoveService stops the systemd unit, deletes the vhost, removes code, and
// finally deletes the DB record. Intended for both cascade-delete and
// explicit user removal.
func (s *ProjectService) RemoveService(ctx context.Context, svcID string) error {
	svc, err := s.GetService(ctx, svcID)
	if err != nil {
		return err
	}
	return s.removeServiceInternal(ctx, svc)
}

func (s *ProjectService) removeServiceInternal(ctx context.Context, svc *models.ProjectService) error {
	if svc.SystemdUnit != "" {
		agent.DeleteSystemdUnit(ctx, svc.SystemdUnit)
	}
	// Only soft-delete install_dir in LEGACY layout. In NEW project-level
	// layout, install_dir is a subdirectory of the shared project clone
	// that sibling services depend on — touching it would break them.
	// The full ProjectDir is cleaned up via Project.Delete (the cascade
	// path) when the LAST service is removed.
	if svc.InstallDir != "" {
		proj, _ := s.loadProject(ctx, svc.ProjectID)
		if proj == nil || proj.ProjectDir == "" {
			// Soft-delete: rename to <dir>.deleted-<ts> so the operator's
			// code is preserved and discoverable via File Manager.
			softDeleteDir(svc.InstallDir, "deleted")
		}
	}
	if _, err := s.db.Collection(database.ColProjectServices).DeleteOne(ctx, bson.M{"_id": svc.ID}); err != nil {
		return err
	}
	// Detach every domain that durably proxied to this service: clear the
	// link and restore each domain's own base vhost so it stops pointing at
	// the now-dead upstream (instead of silently 502-ing). Done after the
	// service row is gone so the set is final.
	for _, ad := range attachedDomainNames(ctx, s.db, svc.ID) {
		clearDomainProxy(ctx, s.db, ad)
		restoreDomainBaseVhost(ctx, s.db, ad)
	}
	// Regenerate the shared vhost WITHOUT the removed service's location
	// block. If this was the last service on that primary domain, delete
	// the vhost entirely so the domain stops answering (or, if any sibling
	// is still present, its contents are preserved).
	if svc.PrimaryDomain != "" {
		proj, err := s.loadProject(ctx, svc.ProjectID)
		if err == nil {
			hasSibling := false
			siblings, _ := s.listServicesForProject(ctx, svc.ProjectID)
			for _, sib := range siblings {
				if sib.PrimaryDomain == svc.PrimaryDomain {
					hasSibling = true
					break
				}
			}
			if hasSibling {
				// Rebuild the vhost from remaining siblings; pass empty
				// caller role so buildMergedVhostSpec only emits DB state.
				_ = s.reconcileVhostFor(ctx, proj, "", svc.PrimaryDomain, nil, "", 0, "")
			} else {
				// No siblings left on this domain — restore it to the
				// original PHP-FPM + public_html shape it had before
				// being attached to a deployed app. SSL preserved.
				// restoreDomainBaseVhost falls back to a placeholder
				// vhost when the domain isn't a registered Domain
				// (e.g. an external domain that pointed straight at the
				// project), so SNI never collapses to the wrong cert.
				restoreDomainBaseVhost(ctx, s.db, svc.PrimaryDomain)
			}
		} else {
			restoreDomainBaseVhost(ctx, s.db, svc.PrimaryDomain)
		}
	}
	return nil
}

// ServiceAction performs a systemctl operation on a single service's backing
// unit. Accepted actions: start, stop, restart. Frontend and static services
// don't have a systemd unit — the call is a no-op with a friendly message so
// "Stop all" works against mixed projects without the UI having to filter.
func (s *ProjectService) ServiceAction(ctx context.Context, svcID, action string) error {
	svc, err := s.GetService(ctx, svcID)
	if err != nil {
		return err
	}
	// systemctl-driven actions need a unit; everything else (pull/install/build)
	// can run for any role.
	switch action {
	case "start", "stop", "restart", "run":
		if svc.SystemdUnit == "" || svc.Role != "backend" {
			return nil // no process to control
		}
	}
	switch action {
	case "start", "stop", "restart":
		if _, err := agent.RunCommand(ctx, "systemctl", action, svc.SystemdUnit); err != nil {
			return fmt.Errorf("systemctl %s: %w", action, err)
		}
		newStatus := map[string]string{"start": "running", "stop": "stopped", "restart": "running"}[action]
		s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": svc.ID}, bson.M{
			"$set": bson.M{"status": newStatus, "updated_at": time.Now()},
		})
		return nil
	case "run":
		// "run" is the operator-friendly alias for "start" — the wizard
		// labels it Run since the start_cmd is what defines what executes.
		if _, err := agent.RunCommand(ctx, "systemctl", "start", svc.SystemdUnit); err != nil {
			return fmt.Errorf("systemctl start: %w", err)
		}
		s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": svc.ID}, bson.M{
			"$set": bson.M{"status": "running", "updated_at": time.Now()},
		})
		return nil
	case "install":
		if svc.InstallCmd == "" {
			return fmt.Errorf("no install command configured for this service")
		}
		proj, err := s.loadProject(ctx, svc.ProjectID)
		if err != nil {
			return fmt.Errorf("load project: %w", err)
		}
		// New layout: install_dir IS the workdir (it's already the subpath
		// dir inside the shared project clone). Legacy layout: workdir is
		// install_dir + subpath.
		wd := svc.InstallDir
		if proj.ProjectDir == "" {
			wd = serviceWorkDir(svc.InstallDir, svc.GitSubpath)
		}
		runtimeBinDir := resolveRuntimeBinDir(resolveServiceAppType(svc.Framework, svc.Role), svc.RuntimeVersion)
		runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := runBuildAsUser(runCtx, svc.User, wd, withNoColor(svc.InstallCmd), runtimeBinDir); err != nil {
			return fmt.Errorf("install failed: %s", summariseBuildOutput(stripANSI(err.Error())))
		}
		return nil
	case "build":
		if svc.BuildCmd == "" {
			return fmt.Errorf("no build command configured for this service")
		}
		proj, err := s.loadProject(ctx, svc.ProjectID)
		if err != nil {
			return fmt.Errorf("load project: %w", err)
		}
		wd := svc.InstallDir
		if proj.ProjectDir == "" {
			wd = serviceWorkDir(svc.InstallDir, svc.GitSubpath)
		}
		runtimeBinDir := resolveRuntimeBinDir(resolveServiceAppType(svc.Framework, svc.Role), svc.RuntimeVersion)
		runCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		if err := runBuildAsUser(runCtx, svc.User, wd, withNoColor(svc.BuildCmd), runtimeBinDir); err != nil {
			return fmt.Errorf("build failed: %s", summariseBuildOutput(stripANSI(err.Error())))
		}
		return nil
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

// ProjectAction fan-outs a systemctl operation across every backend service
// in the project. Errors are accumulated so one broken service doesn't stop
// the rest from being acted on.
func (s *ProjectService) ProjectAction(ctx context.Context, projectID, action string) error {
	oid, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		return fmt.Errorf("invalid project id")
	}
	// "pull" is project-level only — fetches new commits ONCE at
	// proj.ProjectDir so every service in the project sees the new source
	// atomically. Per-service Pull was removed; operators trigger Pull at
	// the project level then optionally Redeploy individual services.
	if action == "pull" {
		proj, err := s.loadProject(ctx, oid)
		if err != nil {
			return fmt.Errorf("load project: %w", err)
		}
		token, err := s.decryptPAT(proj)
		if err != nil {
			return fmt.Errorf("decrypt PAT: %w", err)
		}
		// Build the canonical remote URL with the current token injected.
		remoteURL := proj.GitRepoURL
		if remoteURL == "" {
			// Legacy projects without a project-level repo URL — fall back
			// to the first service's git_repo_url.
			svcs, _ := s.listServicesForProject(ctx, oid)
			for _, sv := range svcs {
				if sv.GitRepoURL != "" {
					remoteURL = sv.GitRepoURL
					break
				}
			}
		}
		if remoteURL == "" {
			return fmt.Errorf("project has no Repository URL")
		}
		if token != "" && strings.HasPrefix(remoteURL, "https://") {
			rest := remoteURL[len("https://"):]
			if at := strings.Index(rest, "@"); at >= 0 && at < strings.Index(rest, "/") {
				rest = rest[at+1:]
			}
			remoteURL = "https://" + token + "@" + rest
		}
		gitOpsDir := proj.ProjectDir
		svcs, _ := s.listServicesForProject(ctx, oid)
		// Detect branch divergence: if services in the project track
		// different branches, a single `reset --hard origin/<branch>`
		// would force everyone onto whichever branch we picked. Group
		// services by branch and sync each group separately.
		branchGroups := map[string][]models.ProjectService{}
		for _, sv := range svcs {
			b := strings.TrimSpace(sv.GitBranch)
			if b == "" {
				b = "main"
			}
			branchGroups[b] = append(branchGroups[b], sv)
		}
		if gitOpsDir == "" {
			// Legacy: each service has its own install_dir + .git, pull
			// independently (their working trees are separate so
			// branch divergence isn't a cross-service issue).
			var firstErr error
			for _, sv := range svcs {
				if sv.InstallDir == "" {
					continue
				}
				if syncErr, _, head := s.inPlaceSync(ctx, sv.InstallDir, remoteURL, sv.GitBranch, token, sv.User); syncErr != nil {
					if firstErr == nil {
						firstErr = fmt.Errorf("svc %s: %s", sv.Name, sanitiseGitError(syncErr, token))
					}
				} else if head != "" {
					s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": sv.ID}, bson.M{
						"$set": bson.M{"last_commit_sha": head, "updated_at": time.Now()},
					})
				}
			}
			return firstErr
		}
		// New layout, single shared clone. If only one branch is in use
		// (the common case), sync once. If multiple branches are in use
		// across services in the same project, fetch ALL branches but
		// reset --hard to whichever one the wizard's primary service
		// targeted; warn that mixing branches in a single project is
		// not supported by the shared-clone model.
		branch := "main"
		for b := range branchGroups {
			branch = b
			break
		}
		if len(branchGroups) > 1 {
			otherBranches := make([]string, 0, len(branchGroups))
			for b := range branchGroups {
				otherBranches = append(otherBranches, b)
			}
			fmt.Fprintf(os.Stderr, "[project %s] services target multiple branches (%v); shared-clone Pull will materialize %q. Split into separate projects if you need per-branch isolation.\n", proj.Slug, otherBranches, branch)
		}
		syncErr, _, head := s.inPlaceSync(ctx, gitOpsDir, remoteURL, branch, token, proj.User)
		if syncErr != nil {
			return fmt.Errorf("git pull: %s", sanitiseGitError(syncErr, token))
		}
		// Stamp the new HEAD on every service that's on the SAME branch
		// we just synced — services on other branches keep their old SHA
		// because they're not actually at the new commit.
		if head != "" {
			s.db.Collection(database.ColProjectServices).UpdateMany(ctx, bson.M{"project_id": oid, "git_branch": branch}, bson.M{
				"$set": bson.M{"last_commit_sha": head, "updated_at": time.Now()},
			})
		}
		return nil
	}
	svcs, err := s.listServicesForProject(ctx, oid)
	if err != nil {
		return err
	}
	var firstErr error
	for _, svc := range svcs {
		if svc.Role != "backend" || svc.SystemdUnit == "" {
			continue
		}
		if err := s.ServiceAction(ctx, svc.ID.Hex(), action); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// RollingRestart restarts every backend service in the project ONE AT A
// TIME, waiting for each to come back to `active` before moving on to
// the next. Designed for zero-downtime restarts on a project with N
// services that can tolerate at most one being down at a time (mid-tier
// load balancers, monorepo backends, microservices).
//
// Behaviour:
//   - Iterates services in deterministic order (sorted by Name) so
//     re-running the action restarts the same service first every time
//     — useful when an operator wants to verify "did service X come up
//     clean" without a moving target.
//   - For each backend service with a SystemdUnit:
//       1. Stamp status="restarting" in mongo (drawer's poll picks it up)
//       2. `systemctl restart <unit>`
//       3. Poll `systemctl is-active <unit>` for up to 15s
//       4. On active → status="running"; on timeout/error →
//          status="error" + stop the rest of the rolling restart so we
//          don't break MORE services
//   - Fire-and-forget: this method launches a goroutine and returns
//     immediately so the HTTP handler can answer the operator's click
//     in <1s. The drawer's existing 3s polling + burst-refresh picks
//     up the per-service status flip in real time.
//
// Distinct from the existing ProjectAction("restart") which fires every
// systemctl restart back-to-back without waiting — that path is fine
// for projects where simultaneous restart is acceptable, and stays
// available under the toolbar's "Restart all" button.
func (s *ProjectService) RollingRestart(ctx context.Context, projectID string) error {
	oid, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		return fmt.Errorf("invalid project id")
	}
	svcs, err := s.listServicesForProject(ctx, oid)
	if err != nil {
		return err
	}
	// Pre-filter to the eligible set + sort so the order is stable
	// across reruns.
	eligible := make([]models.ProjectService, 0, len(svcs))
	for _, sv := range svcs {
		if sv.Role == "backend" && sv.SystemdUnit != "" {
			eligible = append(eligible, sv)
		}
	}
	if len(eligible) == 0 {
		return nil
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].Name < eligible[j].Name })

	go s.runRollingRestart(oid, eligible)
	return nil
}

// runRollingRestart is the background worker. Owns its own context so
// the HTTP handler returning doesn't cancel mid-restart. 15s wait per
// service is enough for the common Node/Go boot-time + the rare slow-
// import case; longer would surface as "frontend looks stuck" in the
// drawer.
func (s *ProjectService) runRollingRestart(oid primitive.ObjectID, eligible []models.ProjectService) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	for _, sv := range eligible {
		// Mark as restarting so the drawer flips this row to a blue
		// "deploying"-style spinner. Re-uses the existing status enum
		// rather than introducing a new value — UI treats "restarting"
		// as a transient state alongside deploying/pending.
		s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": sv.ID}, bson.M{
			"$set": bson.M{"status": "restarting", "updated_at": time.Now()},
		})

		if _, err := agent.RunCommand(ctx, "systemctl", "restart", sv.SystemdUnit); err != nil {
			log.Error().Err(err).Str("unit", sv.SystemdUnit).Str("service", sv.Name).Msg("rolling restart: systemctl restart failed, stopping")
			s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": sv.ID}, bson.M{
				"$set": bson.M{"status": "error", "updated_at": time.Now()},
			})
			return
		}

		// Wait for systemd to report `active` before moving on. Polls
		// every 500ms up to 15s. Most backends boot in 1-3s; a service
		// that takes 15s+ is either crash-looping or starved on a
		// downstream dep — either way the operator wants to stop and
		// investigate before knocking over the next service.
		ok := false
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			res, _ := agent.RunCommand(ctx, "systemctl", "is-active", sv.SystemdUnit)
			if res != nil && strings.TrimSpace(res.Output) == "active" {
				ok = true
				break
			}
		}
		next := "running"
		if !ok {
			next = "error"
		}
		s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": sv.ID}, bson.M{
			"$set": bson.M{"status": next, "updated_at": time.Now()},
		})
		if !ok {
			log.Warn().Str("service", sv.Name).Str("unit", sv.SystemdUnit).Msg("rolling restart: service did not become active within 15s, stopping")
			return
		}
		log.Info().Str("service", sv.Name).Msg("rolling restart: service active, moving to next")
	}
	log.Info().Str("project_id", oid.Hex()).Int("services", len(eligible)).Msg("rolling restart: complete")
}

// RestartAllProjectsRolling walks EVERY project in the panel and runs a
// rolling restart across each one — fully sequential at both levels:
//
//   - Projects are visited in order of creation (oldest first).
//   - Within a project, services restart alphabetically by name (the
//     same stable order as the per-project RollingRestart).
//   - One service is `restarting` at any given moment across the
//     whole panel. Max parallelism = 1.
//
// Stops on the first service that fails to become systemctl `active`
// within 15s so a single broken service doesn't take the whole panel
// down. The failed project's other services + every subsequent
// project are skipped — the operator gets a clear "stopped at <svc>"
// log entry and can investigate.
//
// Fire-and-forget: returns immediately; the work runs in a background
// goroutine. The Deploy Software page's normal 3s polling picks up
// per-service status transitions in real time, so operators see each
// project's services flip through running → restarting → running as
// the panel-wide restart progresses.
//
// Tenant scope: this is a platform-owner-only operation by design
// (wired at the route layer); a vendor cannot use it to disrupt
// another tenant's projects.
func (s *ProjectService) RestartAllProjectsRolling(ctx context.Context) error {
	col := s.db.Collection(database.ColProjects)
	cur, err := col.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}))
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	}
	defer cur.Close(ctx)
	var projects []models.Project
	if err := cur.All(ctx, &projects); err != nil {
		return fmt.Errorf("decode projects: %w", err)
	}
	if len(projects) == 0 {
		return nil
	}
	go s.runRestartAllProjectsRolling(projects)
	return nil
}

// runRestartAllProjectsRolling is the background worker. Owns its own
// 30-minute context because a 20-project panel × 5 services × 5s each
// = ~8 minutes wall time on the happy path; the HTTP handler returning
// must not cancel it.
func (s *ProjectService) runRestartAllProjectsRolling(projects []models.Project) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	for _, proj := range projects {
		svcs, err := s.listServicesForProject(ctx, proj.ID)
		if err != nil {
			log.Warn().Err(err).Str("project", proj.Slug).Msg("panel-wide rolling restart: skipping project (list services failed)")
			continue
		}
		eligible := make([]models.ProjectService, 0, len(svcs))
		for _, sv := range svcs {
			if sv.Role == "backend" && sv.SystemdUnit != "" {
				eligible = append(eligible, sv)
			}
		}
		if len(eligible) == 0 {
			continue
		}
		sort.Slice(eligible, func(i, j int) bool { return eligible[i].Name < eligible[j].Name })

		log.Info().Str("project", proj.Slug).Int("services", len(eligible)).Msg("panel-wide rolling restart: project")

		for _, sv := range eligible {
			s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": sv.ID}, bson.M{
				"$set": bson.M{"status": "restarting", "updated_at": time.Now()},
			})

			if _, err := agent.RunCommand(ctx, "systemctl", "restart", sv.SystemdUnit); err != nil {
				log.Error().Err(err).Str("project", proj.Slug).Str("service", sv.Name).Msg("panel-wide rolling restart: systemctl restart failed, STOPPING")
				s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": sv.ID}, bson.M{
					"$set": bson.M{"status": "error", "updated_at": time.Now()},
				})
				return
			}

			ok := false
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				time.Sleep(500 * time.Millisecond)
				res, _ := agent.RunCommand(ctx, "systemctl", "is-active", sv.SystemdUnit)
				if res != nil && strings.TrimSpace(res.Output) == "active" {
					ok = true
					break
				}
			}
			next := "running"
			if !ok {
				next = "error"
			}
			s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": sv.ID}, bson.M{
				"$set": bson.M{"status": next, "updated_at": time.Now()},
			})
			if !ok {
				log.Warn().Str("project", proj.Slug).Str("service", sv.Name).Msg("panel-wide rolling restart: service did not become active within 15s, STOPPING")
				return
			}
		}
	}
	log.Info().Int("projects", len(projects)).Msg("panel-wide rolling restart: complete")
}

// SetPaused flips the auto-deploy pause switch. Paused projects still accept
// webhooks (so LastWebhookAt keeps updating) but don't enqueue deploys.
func (s *ProjectService) SetPaused(ctx context.Context, id string, paused bool) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid project id")
	}
	_, err = s.db.Collection(database.ColProjects).UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{"paused": paused, "updated_at": time.Now()},
	})
	return err
}

// AddAlias is the legacy unscoped entry point — preserved for callers
// that don't have a project ID handy (e.g. internal transfer replays).
// Tenant scoping still applies via the shared guard.
func (s *ProjectService) AddAlias(ctx context.Context, svcID, domain string) (*models.ProjectService, error) {
	return s.AddAliasWithProject(ctx, "", svcID, domain)
}

// AddAliasWithProject appends an alias domain to a service and re-issues
// the SSL cert so the new domain is included. Rejects duplicates.
//
// Path semantics: when projectIDHex is non-empty (every panel + API
// surface as of 3.1.30), the service is verified to live under that
// project. Mismatched project IDs return ErrServiceProjectMismatch
// (403). When projectIDHex is empty, the project ID isn't checked —
// reserved for legacy in-process callers.
//
// Tenant guard: callers whose role is tenant-scoped must own both
// (a) the project and (b) the linked domain. See
// assertCanLinkAliasOnService for the full rule set.
func (s *ProjectService) AddAliasWithProject(ctx context.Context, projectIDHex, svcID, domain string) (*models.ProjectService, error) {
	domain = sanitizeDomain(domain)
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	svc, proj, err := s.assertCanLinkAliasOnService(ctx, projectIDHex, svcID, domain, true)
	if err != nil {
		return nil, err
	}
	if domain == svc.PrimaryDomain {
		return nil, fmt.Errorf("%s is already the primary domain", domain)
	}
	for _, a := range svc.AliasDomains {
		if a == domain {
			return nil, fmt.Errorf("%s is already an alias", domain)
		}
	}
	// Refuse to link a domain that is already another service's PRIMARY
	// domain. If allowed, this service's vhost would list that name in its
	// server_name under THIS service's cert; nginx resolves the
	// 0.0.0.0:443 server_name collision by keeping the first-loaded block,
	// so the wrong cert gets served for that host (the
	// NET::ERR_CERT_COMMON_NAME_INVALID class). A domain may be exactly one
	// service's primary; aliases must never shadow another primary. This is
	// the link-time guard that stops the pollution at its source (e.g.
	// lamda.* being linked onto betazen's service).
	if cnt, _ := s.db.Collection(database.ColProjectServices).CountDocuments(ctx, bson.M{
		"primary_domain": domain,
		"_id":            bson.M{"$ne": svc.ID},
	}); cnt > 0 {
		return nil, fmt.Errorf("%s is already the primary domain of another service", domain)
	}
	aliases := append([]string{}, svc.AliasDomains...)
	aliases = append(aliases, domain)
	// Persist the new alias list BEFORE reconciling. buildMergedVhostSpec
	// walks sibling services for the same primary and unions their stored
	// alias_domains into the server_name; if we reconciled first, the
	// caller's own DB row would still carry the pre-change list and the
	// outcome would depend on whether "add" or "remove" is accidentally a
	// superset / subset. RemoveService uses the same ordering (DeleteOne
	// before reconcile) for the same reason.
	_, err = s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": svc.ID}, bson.M{
		"$set": bson.M{"alias_domains": aliases, "updated_at": time.Now()},
	})
	if err != nil {
		return nil, err
	}
	sslRes, err := s.reconcileVhostForAliasChange(ctx, proj, svc, aliases, domain)
	if err != nil {
		return nil, err
	}
	// Outbound webhook fan-out — vendor integrations can react to a
	// service gaining a new domain (CDN config, status page, monitoring).
	EmitEvent(ctx, "deploy.linked", LookupTenantIDForDomain(ctx, s.db, domain), map[string]any{
		"project_id":     svc.ProjectID.Hex(),
		"service_id":     svc.ID.Hex(),
		"primary_domain": svc.PrimaryDomain,
		"linked_domain":  domain,
	})
	out, err := s.GetService(ctx, svcID)
	if err != nil {
		return nil, err
	}
	if sslRes != nil {
		out.SSLWarning = sslRes.SSLWarning
		out.SSLCoveredDomains = sslRes.CoveredDomains
	}
	return out, nil
}

// RemoveAlias is the legacy unscoped entry point — preserved for callers
// that don't have a project ID handy. Tenant scoping still applies.
func (s *ProjectService) RemoveAlias(ctx context.Context, svcID, domain string) (*models.ProjectService, error) {
	return s.RemoveAliasWithProject(ctx, "", svcID, domain)
}

// RemoveAliasWithProject deletes an alias. The cert is NOT shrunk (that
// would need certbot delete + re-issue and risks downtime); it simply
// stops being served because nginx no longer lists the alias in
// server_name.
//
// Path semantics + tenant guard match AddAliasWithProject. The
// `mustOwnDomain` flag is FALSE here so an idempotent delete of a
// stale alias the caller doesn't currently own (e.g. a domain that
// was transferred out) doesn't fail with a confusing 403 — the
// project-tenant check still blocks cross-tenant mutations.
func (s *ProjectService) RemoveAliasWithProject(ctx context.Context, projectIDHex, svcID, domain string) (*models.ProjectService, error) {
	domain = sanitizeDomain(domain)
	svc, proj, err := s.assertCanLinkAliasOnService(ctx, projectIDHex, svcID, domain, false)
	if err != nil {
		return nil, err
	}
	kept := make([]string, 0, len(svc.AliasDomains))
	for _, a := range svc.AliasDomains {
		if a != domain {
			kept = append(kept, a)
		}
	}
	// Persist the shrunk alias list BEFORE reconciling. Otherwise
	// buildMergedVhostSpec's sibling walk reads the caller's own row,
	// which still contains the alias we're trying to drop, and unions it
	// back into server_name — so the vhost keeps serving the "removed"
	// alias until something else triggers a reconcile.
	_, err = s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": svc.ID}, bson.M{
		"$set": bson.M{"alias_domains": kept, "updated_at": time.Now()},
	})
	if err != nil {
		return nil, err
	}
	sslRes, err := s.reconcileVhostForAliasChange(ctx, proj, svc, kept, "")
	if err != nil {
		return nil, err
	}
	// Released alias goes back to its registered PHP-FPM vhost (or a
	// placeholder if it isn't a registered Domain). Without this, the
	// alias still resolves to the server but nginx no longer has a
	// matching server_name and SNI silently routes it to whatever 443
	// vhost loads first — typically the wrong site's cert.
	restoreDomainBaseVhost(ctx, s.db, domain)
	// Outbound webhook fan-out — paired with deploy.linked above.
	EmitEvent(ctx, "deploy.unlinked", LookupTenantIDForDomain(ctx, s.db, domain), map[string]any{
		"project_id":       svc.ProjectID.Hex(),
		"service_id":       svc.ID.Hex(),
		"primary_domain":   svc.PrimaryDomain,
		"unlinked_domain":  domain,
	})
	out, err := s.GetService(ctx, svcID)
	if err != nil {
		return nil, err
	}
	if sslRes != nil {
		out.SSLWarning = sslRes.SSLWarning
		out.SSLCoveredDomains = sslRes.CoveredDomains
	}
	return out, nil
}

// AttachDomain is the DURABLE replacement for AddAlias. Instead of pushing
// the domain into the service's editable alias_domains array (where a later
// service edit silently dropped it) + merging it into the primary's
// server_name (where it collided with the per-domain SSL vhost), it stamps
// the link on the DOMAIN row (proxy_service_id + proxy_port) and gives the
// domain its OWN reverse-proxy vhost + cert. The binding now lives on the
// domain, so:
//   - editing the service can never drop it,
//   - SSL / restore / migration rebuild it from the stored port,
//   - two domains on one service never collide on server_name.
//
// The domain MUST already be registered (we proxy its own vhost and need a
// Domain row to own DNS + SSL). Tenant + project guards reuse
// assertCanLinkAliasOnService exactly as AddAlias did.
func (s *ProjectService) AttachDomain(ctx context.Context, projectIDHex, svcID, domain string) (*models.ProjectService, error) {
	domain = sanitizeDomain(domain)
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	svc, _, err := s.assertCanLinkAliasOnService(ctx, projectIDHex, svcID, domain, true)
	if err != nil {
		return nil, err
	}
	if domain == svc.PrimaryDomain {
		return nil, fmt.Errorf("%s is already the primary domain", domain)
	}
	// The domain must exist as a registered Domain — we build ITS own vhost
	// and lean on its DNS zone + SSL ownership. A clear error beats silently
	// creating a half-wired row.
	var dom models.Domain
	if err := s.db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": domain}).Decode(&dom); err != nil {
		return nil, fmt.Errorf("%s is not a registered domain — add it under Domains first, then attach it", domain)
	}
	// Never shadow another service's primary (the wrong-cert / 0.0.0.0:443
	// server_name collision class). A domain may be exactly one service's
	// primary; attachments must never claim another primary.
	if cnt, _ := s.db.Collection(database.ColProjectServices).CountDocuments(ctx, bson.M{
		"primary_domain": domain,
		"_id":            bson.M{"$ne": svc.ID},
	}); cnt > 0 {
		return nil, fmt.Errorf("%s is already the primary domain of another service", domain)
	}
	// Refuse to attach to two services at once — the operator must detach
	// from the first. Re-attaching to the SAME service is an idempotent
	// no-op (just refreshes the port + rebuilds the vhost).
	if dom.ProxyServiceID != nil && !dom.ProxyServiceID.IsZero() && *dom.ProxyServiceID != svc.ID {
		return nil, fmt.Errorf("%s is already attached to another service — detach it there first", domain)
	}

	// 1. Persist the durable link FIRST so any vhost rebuild triggered
	//    elsewhere (SSL sweep, recheck) sees the binding immediately.
	stampDomainProxy(ctx, s.db, domain, svc.ID, svc.Port)
	// 2. Drop any legacy alias_domains entry so the domain isn't ALSO
	//    merged into the primary's server_name (that double-serve is the
	//    "conflicting server name … ignored" bug).
	s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": svc.ID}, bson.M{
		"$pull": bson.M{"alias_domains": domain},
		"$set":  bson.M{"updated_at": time.Now()},
	})
	// 3. Build the domain's own reverse-proxy vhost + cert.
	warning := s.reconcileAttachedDomain(ctx, svc, domain)

	EmitEvent(ctx, "deploy.linked", LookupTenantIDForDomain(ctx, s.db, domain), map[string]any{
		"project_id":     svc.ProjectID.Hex(),
		"service_id":     svc.ID.Hex(),
		"primary_domain": svc.PrimaryDomain,
		"linked_domain":  domain,
	})

	out, err := s.GetService(ctx, svcID)
	if err != nil {
		return nil, err
	}
	if warning != "" {
		out.SSLWarning = warning
	}
	return out, nil
}

// DetachDomain removes the durable service link from a domain and restores
// its standalone base vhost (PHP-FPM placeholder, or whatever its Domain row
// implies). Idempotent. Also clears any legacy alias_domains entry.
func (s *ProjectService) DetachDomain(ctx context.Context, projectIDHex, svcID, domain string) (*models.ProjectService, error) {
	domain = sanitizeDomain(domain)
	svc, _, err := s.assertCanLinkAliasOnService(ctx, projectIDHex, svcID, domain, false)
	if err != nil {
		return nil, err
	}
	clearDomainProxy(ctx, s.db, domain)
	s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": svc.ID}, bson.M{
		"$pull": bson.M{"alias_domains": domain},
		"$set":  bson.M{"updated_at": time.Now()},
	})
	// Rebuild the domain's own base vhost. clearDomainProxy ran first, so
	// applyAttachedProxyVhost (called inside restoreDomainBaseVhost) sees no
	// link and falls through to the placeholder/PHP-FPM template.
	restoreDomainBaseVhost(ctx, s.db, domain)

	EmitEvent(ctx, "deploy.unlinked", LookupTenantIDForDomain(ctx, s.db, domain), map[string]any{
		"project_id":      svc.ProjectID.Hex(),
		"service_id":      svc.ID.Hex(),
		"primary_domain":  svc.PrimaryDomain,
		"unlinked_domain": domain,
	})
	return s.GetService(ctx, svcID)
}

// reconcileAttachedDomain (re)builds an attached domain's own reverse-proxy
// vhost and issues/expands its cert (covering <d> + www.<d> + cname.<d>).
// Returns a human-readable SSL warning when the vhost is up but certbot
// couldn't issue (almost always DNS-not-yet-pointing-here) — empty on full
// success. Mirrors reconcileVhostFor's two-phase HTTP→cert→SSL write so the
// ACME webroot probe succeeds before we emit the 443 block.
func (s *ProjectService) reconcileAttachedDomain(ctx context.Context, svc *models.ProjectService, domain string) string {
	// Serialise cert ops per domain — concurrent certbot runs on one cert
	// corrupt it. Keyed distinctly from the primary's certLock.
	lockRaw, _ := s.certLocks.LoadOrStore("attach:"+domain, &sync.Mutex{})
	lock := lockRaw.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	port := svc.Port
	staticRoot := ""
	if port <= 0 && (svc.Role == "frontend" || svc.Role == "static") {
		if svc.BuildDir != "" {
			staticRoot = svc.BuildDir
		} else if svc.InstallDir != "" {
			staticRoot = svc.InstallDir
		}
	}
	if port <= 0 && staticRoot == "" {
		return fmt.Sprintf("%s attached, but the service isn't built/running yet (no port, no build dir) — deploy the service, then hit Reissue on the SSL page", domain)
	}

	// 1. HTTP vhost first so certbot's /.well-known webroot probe resolves.
	hadCert := agent.LetsEncryptCertExists(domain)
	writeVhost := func(ssl bool) {
		switch {
		case port > 0 && ssl:
			_ = agent.CreateReverseProxyWithSSL(ctx, &agent.VhostConfig{Domain: domain, Port: port})
		case port > 0:
			_ = agent.CreateReverseProxy(ctx, &agent.VhostConfig{Domain: domain, Port: port})
		case ssl:
			_ = agent.CreateStaticVhostWithSSL(ctx, domain, staticRoot, "", "")
		default:
			_ = agent.CreateStaticVhost(ctx, domain, staticRoot)
		}
	}
	writeVhost(hadCert)

	// 2. Issue / expand the cert covering the domain + its www/cname.
	email := s.sslEmail
	if email == "" {
		email = "admin@" + domain
	}
	if err := agent.IssueLetsEncryptMulti(ctx, domain, expandImplicitAliases(domain, nil), email); err != nil {
		fmt.Fprintf(os.Stderr, "[attach %s] certbot failed: %v\n", domain, err)
		return fmt.Sprintf("%s is attached and routing to the service, but Let's Encrypt couldn't issue a cert (%v). Most likely DNS for %s isn't pointing at this server yet — once it is, hit Reissue on the SSL page.", domain, err, domain)
	}

	// 3. Re-emit with the 443 block now that the cert exists, and mark the
	//    domain SSL-active so the Domains page reflects reality.
	writeVhost(true)
	s.db.Collection(database.ColDomains).UpdateOne(ctx, bson.M{"domain": domain}, bson.M{
		"$set": bson.M{"ssl_active": true, "updated_at": time.Now()},
	})
	return ""
}

// DeployAll enqueues every service in a project for redeploy. Returns
// immediately; the worker pool processes them one at a time.
//
// v3.1.68: shared-clone projects (every project created since v3.1.27's
// project-level branch hoist + earlier shared-clone refactor — i.e.
// every install with a populated proj.ProjectDir + proj.GitRepoURL)
// now pull ONCE at the project level and enqueue each service with
// skipPull=true. Pre-3.1.68 the loop enqueued every service with
// skipPull=false, so a 7-service project paid for 7 identical git
// fetches against the same shared clone: the user's Waapi Dev 3.0
// project showed pull 1 = 2.9 s, pull 2 = 20.3 s (filesystem-lock
// contention against service 1's npm install still warming the same
// dir), and so on; ~20-90 s of wasted wall-clock on a 7-service
// "Deploy all", PLUS a correctness window where a mid-deploy push
// would land different services on DIFFERENT commits. Mirrors the
// webhook path (HandleWebhook → runProjectPullAndEnqueue, v3.1.63)
// so both code paths use the exact same one-pull-then-N-rebuilds
// shape. Legacy projects without proj.ProjectDir fall back to the
// pre-3.1.68 per-service-pull behaviour so split-clone installs
// keep working.
func (s *ProjectService) DeployAll(ctx context.Context, projectID string) error {
	oid, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		return fmt.Errorf("invalid project id")
	}
	proj, err := s.loadProject(ctx, oid)
	if err != nil {
		return err
	}
	svcs, err := s.listServicesForProject(ctx, oid)
	if err != nil {
		return err
	}
	if len(svcs) == 0 {
		return nil // nothing to deploy
	}

	// Shared-clone layout: one pull, then enqueue every service with
	// skipPull=true. inPlaceSync runs against proj.ProjectDir; runDeploy
	// per service still reads HEAD via `git rev-parse` (with v3.1.66's
	// safe.directory flag) so each deployment row gets the right
	// commit_sha stamped.
	if proj.ProjectDir != "" && proj.GitRepoURL != "" {
		// Branch source-of-truth as of v3.1.27 is proj.GitBranch.
		// Heal back to the first service's branch on legacy projects
		// where loadProject's auto-heal hasn't fired yet.
		branch := strings.TrimSpace(proj.GitBranch)
		if branch == "" {
			branch = strings.TrimSpace(svcs[0].GitBranch)
		}
		svcIDs := make([]primitive.ObjectID, len(svcs))
		for i, svc := range svcs {
			svcIDs[i] = svc.ID
		}
		// Goroutine matches the webhook flow's shape — DeployAll
		// returns immediately so the WHM "Deploy all" button's
		// 200 OK lands fast; the per-service progress is surfaced
		// via the existing /activity poll the drawer already runs.
		go s.runProjectPullAndEnqueue(proj, oid, branch, "manual", svcIDs)
		return nil
	}

	// Legacy split-clone fallback: every service still pulls into its
	// own InstallDir. Kept for pre-shared-clone installs that haven't
	// yet been re-provisioned; no operator on a current install hits
	// this branch.
	for _, svc := range svcs {
		s.enqueue(svc.ID, "manual", false)
	}
	return nil
}

// DeployService enqueues a single service for redeploy. The skipPull
// argument lets callers request "rebuild + restart only, don't fetch new
// commits" — that's what the per-service Redeploy button does, since git
// pull lives at the project level (one shared clone). Project-level
// "Deploy all" and webhook auto-deploy still pull (skipPull=false).
func (s *ProjectService) DeployService(svcID string, trigger string, skipPull bool) error {
	oid, err := primitive.ObjectIDFromHex(svcID)
	if err != nil {
		return fmt.Errorf("invalid service id")
	}
	s.enqueue(oid, trigger, skipPull)
	return nil
}

func (s *ProjectService) enqueue(svcID primitive.ObjectID, trigger string, skipPull bool) {
	select {
	case s.deployQueue <- deployJob{serviceID: svcID, trigger: trigger, skipPull: skipPull}:
	default:
		// Queue full — mark status and let the user retry. This is a signal
		// that something's stuck (stale worker, hung npm install); better to
		// surface the back-pressure than silently drop.
		s.db.Collection(database.ColProjectServices).UpdateOne(
			context.Background(),
			bson.M{"_id": svcID},
			bson.M{"$set": bson.M{"status": "queue-full"}},
		)
	}
}

// startWorker drains deployQueue. Runs in its own goroutine forever.
//
// Each job is wrapped in a deferred recover so a panic from inside runDeploy
// (e.g. a nil-pointer deref on a half-constructed service record, an agent
// RunCommand returning an unexpected shape) marks the job as errored and
// lets the worker keep pulling subsequent jobs. Without this, one bad job
// kills the goroutine and every future deploy sits in the queue forever.
func (s *ProjectService) startWorker() {
	for job := range s.deployQueue {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "[project worker] panic on service %s: %v\n", job.serviceID.Hex(), r)
					s.db.Collection(database.ColProjectServices).UpdateOne(
						context.Background(),
						bson.M{"_id": job.serviceID},
						bson.M{"$set": bson.M{"status": "error", "updated_at": time.Now()}},
					)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			s.runDeploy(ctx, job)
		}()
	}
}

// runDeploy is the actual work of re-deploying a single service: git pull,
// install, build, restart. Status + log are written back on every boundary so
// the UI's "deploying" indicator is accurate. Each named step's transition
// (in_progress → completed / failed) is also persisted so the WHM detail
// drawer can render a step-by-step timeline with progress percentage.
func (s *ProjectService) runDeploy(ctx context.Context, job deployJob) {
	svc, err := s.GetService(ctx, job.serviceID.Hex())
	if err != nil {
		return
	}
	proj, err := s.loadProject(ctx, svc.ProjectID)
	if err != nil {
		return
	}
	s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": svc.ID}, bson.M{"$set": bson.M{"status": "deploying"}})

	logPath := fmt.Sprintf("/var/log/serverpanel/projects/%s/%s-%d.log", proj.Slug, svc.Name, time.Now().Unix())
	os.MkdirAll(filepath.Dir(logPath), 0755)

	// Pre-declare the step list. Skipped steps stay in the timeline as
	// "skipped" so the operator can see what didn't run and why.
	steps := []models.DeploymentStep{
		{Name: "Pull source from Git", Status: "pending"},
		{Name: "Install dependencies", Status: "pending"},
		{Name: "Build", Status: "pending"},
		{Name: "Restart service", Status: "pending"},
		{Name: "Health check (port bind)", Status: "pending"},
	}
	dep := models.ProjectDeployment{
		ProjectID: proj.ID,
		ServiceID: svc.ID,
		Trigger:   job.trigger,
		Status:    "running",
		StartedAt: time.Now(),
		LogPath:   logPath,
		Steps:     steps,
		Progress:  0,
	}
	res, _ := s.db.Collection(database.ColProjectDeployments).InsertOne(ctx, dep)
	depID, _ := res.InsertedID.(primitive.ObjectID)

	// Step transition helpers. Persist on every boundary so a polling UI
	// (every 1–2s) sees fresh state without any extra wiring.
	totalSteps := len(steps)
	startStep := func(idx int) {
		now := time.Now()
		s.db.Collection(database.ColProjectDeployments).UpdateOne(ctx, bson.M{"_id": depID}, bson.M{
			"$set": bson.M{
				fmt.Sprintf("steps.%d.status", idx):     "in_progress",
				fmt.Sprintf("steps.%d.started_at", idx): now,
				"progress":                              (idx * 100) / totalSteps,
			},
		})
	}
	completeStep := func(idx int, details string) {
		now := time.Now()
		s.db.Collection(database.ColProjectDeployments).UpdateOne(ctx, bson.M{"_id": depID}, bson.M{
			"$set": bson.M{
				fmt.Sprintf("steps.%d.status", idx):       "completed",
				fmt.Sprintf("steps.%d.completed_at", idx): now,
				fmt.Sprintf("steps.%d.details", idx):      details,
				"progress":                                ((idx + 1) * 100) / totalSteps,
			},
		})
	}
	skipStep := func(idx int, reason string) {
		now := time.Now()
		s.db.Collection(database.ColProjectDeployments).UpdateOne(ctx, bson.M{"_id": depID}, bson.M{
			"$set": bson.M{
				fmt.Sprintf("steps.%d.status", idx):       "skipped",
				fmt.Sprintf("steps.%d.completed_at", idx): now,
				fmt.Sprintf("steps.%d.details", idx):      reason,
				"progress":                                ((idx + 1) * 100) / totalSteps,
			},
		})
	}
	failStep := func(idx int, errMsg string) {
		now := time.Now()
		s.db.Collection(database.ColProjectDeployments).UpdateOne(ctx, bson.M{"_id": depID}, bson.M{
			"$set": bson.M{
				fmt.Sprintf("steps.%d.status", idx):       "failed",
				fmt.Sprintf("steps.%d.completed_at", idx): now,
				fmt.Sprintf("steps.%d.error", idx):        errMsg,
			},
		})
	}

	finalize := func(status, errMsg string, commit string) {
		now := time.Now()
		progress := 100
		if status != "running" && status != "success" {
			// On error, freeze progress at whatever step number got us
			// here — don't snap to 100%.
			var existing models.ProjectDeployment
			if e := s.db.Collection(database.ColProjectDeployments).FindOne(ctx, bson.M{"_id": depID}).Decode(&existing); e == nil {
				progress = existing.Progress
			}
		}
		update := bson.M{
			"status":      status,
			"finished_at": now,
			"error_msg":   errMsg,
			"progress":    progress,
		}
		if commit != "" {
			update["commit_sha"] = commit
		}
		s.db.Collection(database.ColProjectDeployments).UpdateOne(ctx, bson.M{"_id": depID}, bson.M{"$set": update})

		// v3.1.70 — deployment.status and service.status describe DIFFERENT
		// things and must use different enums:
		//
		//   deployment.status  ∈ {running, success, error}   ← outcome of THIS deploy
		//   service.status     ∈ {running, deploying, stopped, error, needs_env_vars}
		//                         ← what the service is DOING right now
		//
		// Pre-3.1.66 finalize() wrote "running" to BOTH on the happy path,
		// which the v3.1.66 deployment.status fix corrected to "success" —
		// but the same line also stamped "success" on service.status, and
		// the frontend StatusBadge mapping doesn't know "success" (see
		// frontend/apps/whm/src/pages/DeploySoftwarePage.tsx line ~2807:
		// only "running" / "deploying" / "stopped" / "needs_env_vars" have
		// explicit mappings; anything else, including "success", falls
		// through to "pending" / blue). The visible bug post-3.1.66:
		// every successfully-deployed service rendered as a "pending" badge
		// — the user reported it as "webhook run korar por deploy
		// hochhe na" because all 7 services on Waapi Dev 3.0 looked frozen
		// at pending even though git HEAD + last_commit_sha + the systemd
		// units were all in the right state.
		//
		// Translate here: deployment.status="success" → service.status
		// =="running" (the service IS running post-restart, that's what
		// the status FIELD describes). On error, both stay "error".
		// "running" stays "running" — finalize is called once at the end
		// of every code path so the intermediate state isn't observed.
		svcStatus := status
		if svcStatus == "success" {
			svcStatus = "running"
		}
		svcUpdate := bson.M{"status": svcStatus, "updated_at": now, "last_deployed_at": now}
		if commit != "" {
			svcUpdate["last_commit_sha"] = commit
		}
		s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": svc.ID}, bson.M{"$set": svcUpdate})
	}

	token, err := s.decryptPAT(proj)
	if err != nil {
		finalize("error", err.Error(), "")
		return
	}
	// Rewrite the remote URL with the CURRENT token before every pull. Without
	// this, a rotated PAT (via the "Rotate PAT" button) never takes effect
	// for pull because the origin URL still has the old token baked in from
	// the original clone. Strip credentials out of GitRepoURL first in case
	// the operator pasted a token-in-URL by hand.
	remoteURL := svc.GitRepoURL
	if token != "" && strings.HasPrefix(remoteURL, "https://") {
		// "https://host/path" — drop any existing user:pass@ between scheme
		// and host before injecting the fresh token.
		rest := remoteURL[len("https://"):]
		if at := strings.Index(rest, "@"); at >= 0 && at < strings.Index(rest, "/") {
			rest = rest[at+1:]
		}
		remoteURL = "https://" + token + "@" + rest
	}
	// --- Step 0: pull source (or skip when job.skipPull) ---
	// Per-service Redeploy passes skipPull=true — the operator wants to
	// rebuild + restart on the existing on-disk source, NOT fetch new
	// commits. Project-level Pull is the only place that pulls per the
	// new architecture (one clone per project at proj.ProjectDir).
	gitOpsDir := svc.InstallDir
	if proj.ProjectDir != "" {
		gitOpsDir = proj.ProjectDir
	}
	if job.skipPull {
		// v3.1.69 — two distinct cases the operator needs to be
		// able to tell apart in the per-service progress strip:
		//
		//   (a) projectPullCommit != "" → a project-level
		//       inPlaceSync just landed this exact commit at
		//       proj.ProjectDir (webhook fan-out, manual Deploy
		//       all, or the rare API caller that pipes a commit
		//       in). Render Step 0 as COMPLETED with the SHA so
		//       the operator can see at a glance "yes, the pull
		//       happened — just not separately for each of the
		//       N services". Pre-3.1.69 we used skipStep here
		//       and the operator (e.g. on the Waapi Dev 3.0
		//       project drawer at 08:15 UTC) saw 7 services
		//       each showing "Pull source from Git — skipped"
		//       and concluded "the webhook didn't pull anything"
		//       even though the project-level pull had landed
		//       ca667923... 30 seconds earlier.
		//
		//   (b) projectPullCommit == "" → per-service Redeploy
		//       button click: operator is rebuilding the
		//       existing on-disk source without fetching new
		//       commits, the project-level Pull button is the
		//       way to fetch. Original skipped-step UX preserved.
		if job.projectPullCommit != "" {
			shortSHA := job.projectPullCommit
			if len(shortSHA) > 7 {
				shortSHA = shortSHA[:7]
			}
			completeStep(0, "Pulled at project level — commit "+shortSHA+" (one git fetch shared across every service in this project)")
			// runDeploy below also reads `commit` via git rev-parse to
			// stamp the per-deployment row. Seed it now from the
			// project-pull SHA so the finalize() call records the
			// right value even if the rev-parse later races against
			// a concurrent project-level write.
			// (commit is declared below; we'll let the existing
			// rev-parse code overwrite if it succeeds — that's the
			// authoritative read. This pre-seed only helps the
			// edge case where rev-parse fails and we'd otherwise
			// finalize with commit_sha="" on a successful deploy.)
		} else {
			skipStep(0, "git pull skipped — service-level redeploy uses existing on-disk source (use project-level Pull to fetch new commits)")
		}
	} else {
		startStep(0)
		// inPlaceSync makes gitOpsDir match origin/<branch> WITHOUT renaming
		// or destroying untracked user files. See inPlaceSync() docstring
		// for the full sync logic.
		syncUser := proj.User
		if syncUser == "" {
			syncUser = svc.User
		}
		syncErr, syncOutput, _ := s.inPlaceSync(ctx, gitOpsDir, remoteURL, svc.GitBranch, token, syncUser)
		if syncErr != nil {
			safeOut := syncOutput
			if token != "" {
				safeOut = strings.ReplaceAll(safeOut, token, "***")
			}
			appendLog(logPath, "sync failed: "+sanitiseGitError(syncErr, token)+"\n"+safeOut)
			failStep(0, "git pull failed: "+sanitiseGitError(syncErr, token))
			finalize("error", "git pull failed", "")
			return
		}
	}

	// Pre-seed `commit` from the project-level pull's HEAD when the
	// caller threaded one through (v3.1.69). The git rev-parse below
	// is still authoritative when it succeeds — it overwrites — but
	// this pre-seed means a transient rev-parse failure (filesystem
	// noise, brief git lock) no longer leaves the deployment row's
	// commit_sha empty when we already know the right value.
	commit := strings.TrimSpace(job.projectPullCommit)
	// `-c safe.directory=<dir>` is mandatory here (v3.1.66): the panel
	// runs as root but gitOpsDir lives under /home/<user>/projects/<slug>
	// owned by the project's hosting user. git 2.35+ refuses with
	// "fatal: detected dubious ownership" when uid mismatches the repo
	// owner. Without this flag every rev-parse returned err != nil,
	// commit stayed "", and finalize wrote an empty commit_sha on the
	// deployment record (services still got last_commit_sha set via
	// runProjectPullAndEnqueue's UpdateMany, which masked the bug).
	// Mirrors the same safeArgs prefix used by inPlaceSync below.
	if res, err := agent.RunCommand(ctx, "git", "-c", "safe.directory="+gitOpsDir, "-C", gitOpsDir, "rev-parse", "HEAD"); err == nil {
		if h := strings.TrimSpace(res.Output); h != "" {
			commit = h
		}
	}
	// v3.1.71: completeStep(0, "Pulled latest from...") is gated on
	// !job.skipPull. Pre-3.1.71 this call was unconditional and
	// OVERWROTE the IF branch's completeStep(0, "Pulled at project
	// level — commit X (one git fetch shared across every service in
	// this project)") two lines later in the same runDeploy run.
	// Net effect: my v3.1.69 step-0 message never actually landed on
	// the deployment row even when skipPull=true and projectPullCommit
	// was set — diagnostic logs at enqueue time and inside runDeploy
	// both showed the right values, but the row's step[0].details
	// always ended up the per-service form. The `commit` calculation
	// above still runs unconditionally because finalize() reads it
	// for the deployment row's commit_sha and the service's
	// last_commit_sha regardless of which branch produced step 0.
	if !job.skipPull {
		pullDetails := "Pulled latest from " + svc.GitBranch
		if commit != "" {
			shortSHA := commit
			if len(shortSHA) > 7 {
				shortSHA = shortSHA[:7]
			}
			pullDetails += " (" + shortSHA + ")"
		}
		completeStep(0, pullDetails)
	}

	runtimeBinDir := resolveRuntimeBinDir(resolveServiceAppType(svc.Framework, svc.Role), svc.RuntimeVersion)
	// In the NEW project-level-clone layout, svc.InstallDir IS the subpath
	// dir inside the shared clone (no double-nesting). In LEGACY layout,
	// the install_dir holds the full repo and the app lives at
	// install_dir/<subpath>, so we resolve to that.
	workDir := svc.InstallDir
	if proj.ProjectDir == "" {
		workDir = serviceWorkDir(svc.InstallDir, svc.GitSubpath)
	}
	// Pre-clean any stray package-lock.json / pnpm-lock.yaml / yarn.lock the
	// previous deploy of a sibling service (or a tool like Next.js doing
	// workspace auto-detect) may have leaked into the project's PARENT dir.
	// When such files exist there, Next.js / pnpm / yarn walk UP from
	// install_dir, find the parent's lockfile, and treat the parent as the
	// workspace root — leading to "no production build in .next" or wrong
	// node_modules resolution at runtime. Only the per-service workDir
	// should own a lockfile; the wrapper /home/<user>/projects/<slug>/
	// shouldn't.
	if parent := filepath.Dir(svc.InstallDir); parent != "" && parent != "/" && strings.Contains(parent, "/projects/") && parent != proj.ProjectDir {
		agent.RunCommand(ctx, "rm", "-f",
			filepath.Join(parent, "package-lock.json"),
			filepath.Join(parent, "pnpm-lock.yaml"),
			filepath.Join(parent, "yarn.lock"),
			filepath.Join(parent, "package.json"),
		)
	}
	// --- Step 1: install dependencies ---
	if svc.InstallCmd != "" {
		startStep(1)
		if err := runBuildAsUser(ctx, svc.User, workDir, withNoColor(svc.InstallCmd), runtimeBinDir); err != nil {
			clean := stripANSI(err.Error())
			appendLog(logPath, "install: "+clean)
			failStep(1, summariseBuildOutput(clean))
			finalize("error", summariseBuildOutput(clean), commit)
			return
		}
		completeStep(1, svc.InstallCmd)
	} else {
		skipStep(1, "no install command configured")
	}
	// --- Step 2: build ---
	if svc.BuildCmd != "" {
		startStep(2)
		if err := runBuildAsUser(ctx, svc.User, workDir, withNoColor(svc.BuildCmd), runtimeBinDir); err != nil {
			clean := stripANSI(err.Error())
			appendLog(logPath, "build: "+clean)
			failStep(2, summariseBuildOutput(clean))
			finalize("error", summariseBuildOutput(clean), commit)
			return
		}
		completeStep(2, svc.BuildCmd)
	} else {
		skipStep(2, "no build command configured")
	}
	// --- Step 3: restart service ---
	if svc.Role == "backend" && svc.SystemdUnit != "" {
		startStep(3)
		// Self-heal a missing unit file. The create path DELETES the unit
		// when the first start didn't bind a port within 20s (to halt the
		// crash-loop) — but a redeploy here only `systemctl restart`s, which
		// silently no-ops on a non-existent unit, so the health check below
		// then reports a phantom "port never opened" crash forever and the
		// service can never recover from the UI (its files build & run fine).
		// If the unit is gone, rebuild it from the stored service config
		// before restarting.
		unitPath := "/etc/systemd/system/" + svc.SystemdUnit + ".service"
		if _, statErr := os.Stat(unitPath); os.IsNotExist(statErr) {
			if startCmd := renderStartCmd(svc.StartCmd, svc.Port); strings.TrimSpace(startCmd) != "" {
				env := map[string]string{}
				for k, v := range svc.EnvVars {
					env[k] = v
				}
				env["PORT"] = fmt.Sprintf("%d", svc.Port)
				if runtimeBinDir != "" {
					env["PATH"] = runtimeBinDir + ":/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
				}
				if err := agent.CreateSystemdUnit(ctx, svc.SystemdUnit, svc.User, workDir, startCmd, env); err != nil {
					appendLog(logPath, "recreate unit: "+err.Error())
				} else {
					appendLog(logPath, "recreated missing systemd unit "+svc.SystemdUnit)
				}
			}
		}
		agent.RunCommand(ctx, "systemctl", "restart", svc.SystemdUnit)
		completeStep(3, "systemctl restart "+svc.SystemdUnit)
		// --- Step 4: health check (port bind) ---
		startStep(4)
		// Re-detect the listening port in case the app's hardcoded listen
		// port changed between deploys (or was previously wrong in the DB).
		// If it differs from what we had, update the DB AND regenerate the
		// nginx vhost so the reverse proxy keeps matching reality.
		if detected := detectListeningPort(ctx, svc.SystemdUnit, 20*time.Second, svc.Port); detected > 0 && detected != svc.Port {
			s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": svc.ID}, bson.M{
				"$set": bson.M{"port": detected, "updated_at": time.Now()},
			})
			svc.Port = detected
			if proj, err := s.loadProject(ctx, svc.ProjectID); err == nil {
				_ = s.reconcileVhostFor(ctx, proj, svc.Role, svc.PrimaryDomain, svc.AliasDomains, svc.PathPrefix, detected, svc.BuildDir)
			}
			completeStep(4, fmt.Sprintf("Listening on :%d (port reconciled)", detected))
		} else if waitForPort(svc.Port, 4*time.Second) {
			completeStep(4, fmt.Sprintf("Listening on :%d", svc.Port))
		} else {
			// Port never bound — service is almost certainly crash-looping.
			// Surface this as a real failure instead of a green checkmark
			// with a misleading "will retry" hint, since nginx will return
			// 502 Bad Gateway forever otherwise. Stop the unit so systemd
			// stops the restart loop (otherwise the journal keeps growing
			// and the host keeps spawning processes), then attach the last
			// few journal lines + a hint when we recognise the failure
			// pattern (missing dev-dependency, missing module, etc.).
			agent.RunCommand(context.Background(), "systemctl", "stop", svc.SystemdUnit)
			tail := tailJournal(ctx, svc.SystemdUnit, 30)
			summary := fmt.Sprintf("Port :%d never opened — service crashed on start (unit stopped to halt the restart loop).", svc.Port)
			if hint := diagnoseStartCrash(tail); hint != "" {
				summary += "\n\n" + hint
			}
			if tail != "" {
				summary += "\n\nLast journal output:\n" + tail
			}
			failStep(4, summary)
			finalize("error", summary, commit)
			return
		}
	} else {
		skipStep(3, "static service — no systemd unit to restart")
		skipStep(4, "static service — no port to health-check")
	}
	// v3.1.66: was `finalize("running", "", commit)` — every successful
	// deploy stayed at status="running" forever in project_deployments,
	// and the WHM Deploy-Software activity list rendered finished
	// deploys with the in-progress spinner indefinitely. The frontend
	// has a fallback (treats `status="running" && finished_at!=nil` as
	// success at line 2978 in DeploySoftwarePage.tsx) but the badge,
	// success-count, and "last 5 deploys" coloring all keyed off the
	// raw status string and showed wrong. Finalizing as "success"
	// matches the value the frontend's positive-path renderer expects
	// (line 2770: `dep.status === "success"`). Errors continue to
	// finalize as "error" via the earlier return paths.
	finalize("success", "", commit)
}

// lookupDomainOwner returns the system user that owns a registered domain,
// or empty string when the domain isn't in the panel's domains collection.
// Used by Provision/AddService to default the service's User to the same
// account that owns the primary_domain — keeps the project files under
// /home/<existing-user>/ instead of spawning a throwaway sp-<slug>-<hash>
// account just because the wizard didn't ask for a user explicitly.
// recloneInPlace recovers from a missing or corrupted .git directory by
// soft-renaming the current install_dir aside and cloning a fresh checkout
// (with the configured GitSubpath honoured) into the original path. Any
// user-uploaded files in the old dir survive under <installDir>.no-git-<ts>
// for manual recovery — we never rm -rf user data.
// inPlaceSync makes gitOpsDir match origin/<branch> WITHOUT renaming the
// directory or destroying untracked user files. Three steps:
//
//  1. Ensure .git exists. If missing, `git init` IN gitOpsDir and add the
//     remote — this restores version control on a dir that has user files
//     in it, no rename of the parent.
//  2. `git fetch origin <branch>` to bring refs up to date.
//  3. `git reset --hard origin/<branch>` to make the working tree match.
//     This OVERWRITES tracked files to the remote state (which is what the
//     operator wanted from "git pull") and PRESERVES untracked files like
//     uploads, .env, node_modules — git's reset --hard never touches those.
//
// Returns (error, combined output, HEAD SHA after sync). The combined
// output is the operator-facing log line.
//
// When user is non-empty, the working tree is chowned to user:user after
// a successful reset. Without this, the next build/install step (which
// runs as the project user via sudo -u) would fail with EACCES trying
// to overwrite tracked files like package-lock.json that git wrote as
// root during the reset.
func (s *ProjectService) inPlaceSync(ctx context.Context, gitOpsDir, remoteURL, branch, token, user string) (error, string, string) {
	if gitOpsDir == "" {
		return fmt.Errorf("gitOpsDir required"), "", ""
	}
	if branch == "" {
		branch = "main"
	}
	// Serialise git ops per dir — N parallel runDeploy invocations from
	// "Deploy all" or N webhook-triggered services on the same projectDir
	// would otherwise race fetch+reset and leave the tree half-applied.
	muIface, _ := s.gitLocks.LoadOrStore(gitOpsDir, &sync.Mutex{})
	mu := muIface.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	// safeArgs prefixes every git invocation with -c safe.directory=<dir>
	// so the panel (running as root) can operate on dirs owned by the
	// project user (e.g. jagoanaandadhara) without git refusing with
	// 'fatal: detected dubious ownership'. Without this, every pull
	// against a user-owned dir fails until someone manually runs
	// `git config --global --add safe.directory <dir>` on the host.
	safeArgs := func(rest ...string) []string {
		return append([]string{"-c", "safe.directory=" + gitOpsDir, "-C", gitOpsDir}, rest...)
	}
	var allOut strings.Builder
	// Step 1: ensure .git exists.
	gitDirExists := false
	if r, e := agent.RunCommand(ctx, "git", safeArgs("rev-parse", "--git-dir")...); e == nil && r != nil && strings.TrimSpace(r.Output) != "" {
		gitDirExists = true
	}
	if !gitDirExists {
		// Make sure the dir itself exists (operator may have removed it).
		agent.RunCommand(ctx, "mkdir", "-p", gitOpsDir)
		if r, e := agent.RunCommand(ctx, "git", safeArgs("init")...); e != nil {
			if r != nil {
				allOut.WriteString(r.Output + "\n" + r.Error + "\n")
			}
			return fmt.Errorf("git init: %w", e), allOut.String(), ""
		}
		if r, e := agent.RunCommand(ctx, "git", safeArgs("remote", "add", "origin", remoteURL)...); e != nil {
			// Remote may already exist from a previous half-init; set-url
			// covers both add-or-update.
			agent.RunCommand(ctx, "git", safeArgs("remote", "set-url", "origin", remoteURL)...)
			if r != nil {
				allOut.WriteString(r.Output + "\n")
			}
		}
	} else {
		// Always rotate the remote URL so a freshly-rotated PAT takes effect.
		agent.RunCommand(ctx, "git", safeArgs("remote", "set-url", "origin", remoteURL)...)
	}
	// Step 2: fetch.
	fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	r, fetchErr := agent.RunCommand(fetchCtx, "git", safeArgs("fetch", "--depth=1", "origin", branch)...)
	cancel()
	if r != nil {
		allOut.WriteString(r.Output + "\n" + r.Error + "\n")
	}
	if fetchErr != nil {
		return fmt.Errorf("git fetch: %w", fetchErr), allOut.String(), ""
	}
	// Step 3: reset --hard to FETCH_HEAD (== origin/branch we just fetched).
	// This overwrites tracked files but leaves untracked files alone.
	r, resetErr := agent.RunCommand(ctx, "git", safeArgs("reset", "--hard", "FETCH_HEAD")...)
	if r != nil {
		allOut.WriteString(r.Output + "\n" + r.Error + "\n")
	}
	if resetErr != nil {
		return fmt.Errorf("git reset --hard: %w", resetErr), allOut.String(), ""
	}
	// Git ran as root, so any files it wrote during the reset are now
	// root-owned. Restore ownership to the project user so the subsequent
	// install/build step (which runs via `sudo -u user`) can overwrite
	// lockfiles like package-lock.json without EACCES.
	if user != "" {
		if chownErr := chownRecursive(ctx, gitOpsDir, user); chownErr != nil {
			return fmt.Errorf("chown after sync: %w", chownErr), allOut.String(), ""
		}
	}
	// Read HEAD for the deployment record.
	head := ""
	if r, e := agent.RunCommand(ctx, "git", safeArgs("rev-parse", "HEAD")...); e == nil && r != nil {
		head = strings.TrimSpace(r.Output)
	}
	return nil, allOut.String(), head
}

func (s *ProjectService) recloneInPlace(ctx context.Context, svc *models.ProjectService, remoteURL, token string) error {
	if svc.InstallDir == "" {
		return fmt.Errorf("service has no install_dir")
	}
	// agent.GitClone injects the token itself, so strip any user:pass@ that
	// callers may have pre-baked into remoteURL. Without this, GitClone would
	// produce "https://TOKEN@TOKEN@host/..." which curl rejects with
	// "URL rejected: Bad hostname".
	cleanURL := remoteURL
	if strings.HasPrefix(cleanURL, "https://") {
		rest := cleanURL[len("https://"):]
		if slash := strings.Index(rest, "/"); slash > 0 {
			if at := strings.Index(rest[:slash], "@"); at >= 0 {
				rest = rest[at+1:]
				cleanURL = "https://" + rest
			}
		}
	}
	// In NEW project-level layout, the re-clone target is proj.ProjectDir
	// (the shared clone for the whole project) — every service is just a
	// subdirectory. In LEGACY layout, the target is svc.InstallDir.
	proj, projErr := s.loadProject(ctx, svc.ProjectID)
	cloneTarget := svc.InstallDir
	if projErr == nil && proj.ProjectDir != "" {
		cloneTarget = proj.ProjectDir
	}
	tmp := cloneTarget + ".reclone"
	agent.RunCommand(ctx, "rm", "-rf", tmp)
	cloneCtx, cloneCancel := context.WithTimeout(ctx, 5*time.Minute)
	cloneErr := agent.GitClone(cloneCtx, cleanURL, svc.GitBranch, tmp, token)
	cloneCancel()
	if cloneErr != nil {
		agent.RunCommand(ctx, "rm", "-rf", tmp)
		return fmt.Errorf("git clone: %w", cloneErr)
	}
	// Validate the subpath exists in the repo.
	if sub := strings.Trim(svc.GitSubpath, "/"); sub != "" {
		if r, err := agent.RunCommand(ctx, "test", "-d", filepath.Join(tmp, sub)); err != nil || r == nil {
			agent.RunCommand(ctx, "rm", "-rf", tmp)
			return fmt.Errorf("git_subpath %q does not exist in repo", svc.GitSubpath)
		}
	}
	// Preserve the existing dir for recovery rather than rm -rf'ing user data.
	softDeleteDir(cloneTarget, "no-git")
	if _, err := agent.RunCommand(ctx, "mkdir", "-p", cloneTarget); err != nil {
		return fmt.Errorf("recreate clone target: %w", err)
	}
	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("shopt -s dotglob && mv %s/* %s/ && rm -rf %s", tmp, cloneTarget, tmp)); err != nil {
		return fmt.Errorf("stage checkout: %w", err)
	}
	chownTarget := cloneTarget
	chownUser := svc.User
	if projErr == nil && proj.User != "" {
		chownUser = proj.User
	}
	if chownUser != "" {
		if err := chownRecursive(ctx, chownTarget, chownUser); err != nil {
			return fmt.Errorf("chown: %w", err)
		}
	}
	// Set the remote URL with the current token so subsequent pulls work.
	agent.RunCommand(ctx, "git", "-C", cloneTarget, "remote", "set-url", "origin", remoteURL)
	// LEGACY layout only: the existing systemd unit's WorkingDirectory may
	// still point at svc.InstallDir from the old "stripped" layout. Recreate
	// the unit so it points at install_dir/<subpath> where the app actually
	// lives after the no-strip move.
	if (projErr != nil || proj.ProjectDir == "") && svc.SystemdUnit != "" && svc.Role == "backend" && strings.Trim(svc.GitSubpath, "/") != "" {
		workDir := serviceWorkDir(svc.InstallDir, svc.GitSubpath)
		startCmd := renderStartCmd(svc.StartCmd, svc.Port)
		env := map[string]string{}
		for k, v := range svc.EnvVars {
			env[k] = v
		}
		if svc.Port > 0 {
			env["PORT"] = fmt.Sprintf("%d", svc.Port)
		}
		if err := agent.CreateSystemdUnit(ctx, svc.SystemdUnit, svc.User, workDir, startCmd, env); err != nil {
			return fmt.Errorf("recreate systemd unit with new workdir: %w", err)
		}
	}
	return nil
}

func (s *ProjectService) lookupDomainOwner(ctx context.Context, domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return ""
	}
	var d models.Domain
	if err := s.db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": domain}).Decode(&d); err != nil {
		return ""
	}
	return strings.TrimSpace(d.User)
}

// assertProjectDomainOwnership enforces the "every domain belongs to
// the project's linked vendor" invariant the WHM Add Service modal
// already enforces client-side by filtering the dropdown. The bulk-
// upload path and any direct API caller bypass that UI filter, so
// we re-check here at the service layer.
//
// Rules (only applied when proj.User is non-empty):
//
//   - primary_domain MUST be registered in the panel
//   - primary_domain's owner MUST equal proj.User
//   - every alias domain MUST be registered + owned by proj.User
//   - if the caller supplied req.User explicitly, it MUST equal proj.User
//     (a service can't legitimately split between two vendor /home dirs)
//   - on success: req.User is pinned to proj.User so downstream
//     install_dir / systemd unit / .env file all root at the correct
//     home directory
//
// For legacy projects with no linked vendor (proj.User == "") the
// function is a no-op — the caller's existing fallback resolves req.User
// from the primary domain's owner.
//
// ownerLookup is injected so a unit test can exercise the rule matrix
// without standing up a Mongo handle.
func assertProjectDomainOwnership(proj *models.Project, req *models.AddServiceRequest, ownerLookup func(domain string) string) error {
	if proj == nil || strings.TrimSpace(proj.User) == "" {
		// Legacy / un-pinned project: skip the guard so we don't
		// break the next AddService call on a project that pre-dates
		// the user-hoist refactor. New projects always set proj.User
		// at Provision time, so this branch is rarely reached.
		return nil
	}
	primary := strings.ToLower(strings.TrimSpace(req.PrimaryDomain))
	if primary == "" {
		// The validator already requires primary_domain at the request
		// level; this is a belt-and-braces guard against a future
		// schema change that loosens it.
		return fmt.Errorf("primary_domain is required")
	}
	owner := strings.TrimSpace(ownerLookup(primary))
	if owner == "" {
		return fmt.Errorf("primary_domain %q is not registered in the panel — add it under Domains first, then retry", primary)
	}
	if owner != proj.User {
		return fmt.Errorf("primary_domain %q belongs to vendor %q but this project is linked to vendor %q — pick a domain owned by %q or move the domain first", primary, owner, proj.User, proj.User)
	}
	for _, alias := range req.AliasDomains {
		alias = strings.ToLower(strings.TrimSpace(alias))
		if alias == "" {
			continue
		}
		aliasOwner := strings.TrimSpace(ownerLookup(alias))
		if aliasOwner == "" {
			return fmt.Errorf("alias %q is not registered in the panel — add it under Domains first, then retry", alias)
		}
		if aliasOwner != proj.User {
			return fmt.Errorf("alias %q belongs to vendor %q but this project is linked to vendor %q", alias, aliasOwner, proj.User)
		}
	}
	if u := strings.TrimSpace(req.User); u != "" && u != proj.User {
		return fmt.Errorf("user %q does not match the project's linked vendor %q — leave the user column blank or set it to %q", u, proj.User, proj.User)
	}
	// Pin the service to the project's vendor — every domain has been
	// validated to belong to proj.User, so this is the canonical value
	// regardless of whether the caller supplied one.
	req.User = proj.User
	return nil
}

// ProjectActivity is the aggregate "what's happening with this project"
// payload returned by GET /projects/:id/activity. The WHM detail drawer
// uses it to render one compact card answering the operator's most
// common questions: when did the code last change, when did it last
// deploy (and was it manual or webhook-triggered), how many recent
// deploys failed, and how is each running service doing right now.
type ProjectActivity struct {
	LastCommit *CommitInfo                    `json:"last_commit,omitempty"`
	Deploys    DeployStats                    `json:"deploys"`
	Webhook    WebhookActivity                `json:"webhook"`
	Recent     []models.ProjectDeployment     `json:"recent_deployments"`
	Runtime    map[string]ServiceRuntimeStats `json:"runtime"`
}

type CommitInfo struct {
	SHA     string    `json:"sha"`
	Short   string    `json:"short"`
	Message string    `json:"message"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
}

type DeployStats struct {
	Total      int                        `json:"total"`
	Successful int                        `json:"successful"`
	Failed     int                        `json:"failed"`
	LastAt     *time.Time                 `json:"last_at,omitempty"`
	LastBy     string                     `json:"last_by"` // trigger: manual / webhook / first-deploy / api
	LastManual *models.ProjectDeployment  `json:"last_manual,omitempty"`
	LastAuto   *models.ProjectDeployment  `json:"last_auto,omitempty"`
}

type WebhookActivity struct {
	LastAt    *time.Time `json:"last_at,omitempty"`
	LastEvent string     `json:"last_event"`
	Configured bool      `json:"configured"`
}

type ServiceRuntimeStats struct {
	ServiceID  string `json:"service_id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	UnitState  string `json:"unit_state"` // active / inactive / failed (from systemctl)
	UptimeSec  int64  `json:"uptime_sec"`
	MainPID    string `json:"main_pid"`
	MemoryMB   int64  `json:"memory_mb"`
	NumRestarts int   `json:"num_restarts"`
}

// Activity returns the aggregate activity payload for a project.
//
// `limit` controls how many recent deployments come back in the `recent`
// slice. 0 or negative = default (10). Capped at 500 so a runaway query
// against a project with 50 000 historical deploys doesn't blow the
// payload or the wire. The total / successful / failed counters are
// derived from the SAME slice — accurate for projects with ≤ limit
// deploys, an under-estimate for older projects when limit is small.
// Callers that want exact lifetime counts should call with a high
// limit (the UI's "Show all" path) or accept the visible-window
// approximation.
func (s *ProjectService) Activity(ctx context.Context, projectID string, limit int) (*ProjectActivity, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 500 {
		limit = 500
	}
	oid, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		return nil, fmt.Errorf("invalid project id")
	}
	proj, err := s.loadProject(ctx, oid)
	if err != nil {
		return nil, err
	}
	out := &ProjectActivity{
		Webhook: WebhookActivity{
			LastAt:     proj.LastWebhookAt,
			LastEvent:  proj.LastWebhookEvent,
			Configured: proj.GitHubPATEncrypted != nil && len(proj.GitHubPATEncrypted) > 0,
		},
		Runtime: map[string]ServiceRuntimeStats{},
		// Always non-nil slice so JSON marshals as [] not null — the WHM UI
		// calls .length on this without nil-checking.
		Recent: []models.ProjectDeployment{},
	}

	// Exact lifetime counters via a server-side count (cheap, indexed on
	// project_id). Pre-3.1.80 the counters were derived from the
	// (capped-at-50) recent slice, so a project with 200 deploys
	// reported "47 successful" when reality was "189 successful". This
	// path is sub-millisecond against a normal index.
	//
	// 3.1.86 — fixed a long-standing accuracy bug: "running" was being
	// counted as Successful, inflating the success number whenever a
	// deploy was in flight at query time. Status "running" means
	// "deploy in progress" — not finished, so it doesn't belong in
	// either bucket. We now count only the terminal states ("success",
	// "error"/"failed"); the difference between Total and
	// Successful+Failed is the in-flight set, and matches what the
	// operator sees in the per-service progress timeline.
	depCol := s.db.Collection(database.ColProjectDeployments)
	if total, terr := depCol.CountDocuments(ctx, bson.M{"project_id": oid}); terr == nil {
		out.Deploys.Total = int(total)
	}
	if ok, oerr := depCol.CountDocuments(ctx, bson.M{
		"project_id": oid,
		"status":     "success",
	}); oerr == nil {
		out.Deploys.Successful = int(ok)
	}
	if bad, berr := depCol.CountDocuments(ctx, bson.M{
		"project_id": oid,
		"status":     bson.M{"$in": bson.A{"error", "failed"}},
	}); berr == nil {
		out.Deploys.Failed = int(bad)
	}

	// 3.1.86 — load services up-front so we can enrich every
	// recent_deployments row with the service's name + role for the
	// UI's "which service?" chip, and reuse the same list for the
	// last-commit fallback + runtime stats blocks below. One mongo
	// round-trip instead of three.
	svcs, _ := s.listServicesForProject(ctx, oid)
	svcByID := make(map[primitive.ObjectID]models.ProjectService, len(svcs))
	for _, sv := range svcs {
		svcByID[sv.ID] = sv
	}

	// Recent slice — the bounded list the UI renders. Sorted newest-
	// first; honours the caller's `limit` so the "Show all (N)" link
	// can pull the full window in one round-trip without changing the
	// endpoint shape.
	cur, _ := depCol.Find(ctx, bson.M{"project_id": oid},
		options.Find().SetSort(bson.D{{Key: "started_at", Value: -1}}).SetLimit(int64(limit)))
	var deps []models.ProjectDeployment
	if cur != nil {
		_ = cur.All(ctx, &deps)
		cur.Close(ctx)
	}
	// Backfill LastManual / LastAuto from the visible window. If the
	// last manual/auto deploy is older than `limit` rows back, these
	// stay nil and the UI's "Last manual" card just doesn't render —
	// strictly better than the pre-3.1.80 behaviour of going stale at
	// row 50.
	//
	// Also stamp ServiceName + ServiceRole on each row from svcByID so
	// the UI can render a "which service?" chip without a second
	// /services lookup. Missing service (deleted, archived) → fields
	// stay empty and the UI just omits the chip.
	for i := range deps {
		if sv, ok := svcByID[deps[i].ServiceID]; ok {
			deps[i].ServiceName = sv.Name
			deps[i].ServiceRole = sv.Role
		}
		if out.Deploys.LastManual == nil && deps[i].Trigger == "manual" {
			out.Deploys.LastManual = &deps[i]
		}
		if out.Deploys.LastAuto == nil && (deps[i].Trigger == "webhook" || deps[i].Trigger == "auto") {
			out.Deploys.LastAuto = &deps[i]
		}
	}
	if len(deps) > 0 {
		out.Deploys.LastAt = &deps[0].StartedAt
		out.Deploys.LastBy = deps[0].Trigger
		out.Recent = deps
	}
	// out.Recent left as the [] sentinel set above when len(deps) == 0.

	// Last commit. In NEW project-level layout the .git lives at
	// proj.ProjectDir, so check there first. In LEGACY layout each service
	// has its own clone — fall back to scanning service install_dirs.
	if proj.ProjectDir != "" {
		if ci := readGitHeadCommit(ctx, proj.ProjectDir); ci != nil {
			out.LastCommit = ci
		}
	}
	for _, sv := range svcs {
		if out.LastCommit != nil {
			break
		}
		if sv.InstallDir == "" {
			continue
		}
		ci := readGitHeadCommit(ctx, sv.InstallDir)
		if ci != nil {
			out.LastCommit = ci
			break
		}
	}

	// Per-service runtime stats from systemd. Cheap — one systemctl show
	// per service, no extra DB queries.
	for _, sv := range svcs {
		if sv.SystemdUnit == "" {
			continue
		}
		out.Runtime[sv.ID.Hex()] = readSystemdStats(ctx, sv)
	}

	return out, nil
}

// LatestDeployment returns the most recent ProjectDeployment row for a
// service — including the per-step timeline. Used by the WHM "deploy in
// progress" drawer to render real-time progress without subscribing to a
// WebSocket (the UI polls this every 1.5s while status is "running").
func (s *ProjectService) LatestDeployment(ctx context.Context, serviceID string) (*models.ProjectDeployment, error) {
	oid, err := primitive.ObjectIDFromHex(serviceID)
	if err != nil {
		return nil, fmt.Errorf("invalid service id")
	}
	var dep models.ProjectDeployment
	opts := options.FindOne().SetSort(bson.D{{Key: "started_at", Value: -1}})
	if err := s.db.Collection(database.ColProjectDeployments).FindOne(ctx, bson.M{"service_id": oid}, opts).Decode(&dep); err != nil {
		return nil, err
	}
	return &dep, nil
}

// stampWebhookError records a *failed* webhook delivery on the Project so the
// UI badge can render "Signature mismatch · 2m ago" instead of the misleading
// "Waiting for first delivery" pre-3.1.73 saw for every rejected delivery.
// We deliberately don't touch last_webhook_at here — keeping that field
// reserved for verified deliveries preserves its meaning as "this URL+secret
// pair definitely works". Best-effort: failures here are logged but never
// propagated, since the calling webhook handler already has its own error
// path.
func (s *ProjectService) stampWebhookError(ctx context.Context, oid primitive.ObjectID, reason string) {
	now := time.Now()
	if _, err := s.db.Collection(database.ColProjects).UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{
			"last_webhook_error":    reason,
			"last_webhook_error_at": now,
		},
	}); err != nil {
		log.Warn().Err(err).Str("project_id", oid.Hex()).Msg("failed to stamp webhook error")
	}
}

// HandleWebhook verifies the GitHub HMAC signature then enqueues redeploys
// for every service whose GitSubpath matches a path in the commit's
// added/modified/removed list. A missing or empty `commits` field (e.g. a
// ping event) triggers nothing — only push events redeploy.
//
// Every signature-verified delivery bumps Project.LastWebhookAt (including
// pings, paused projects, pushes to non-matching branches) so the UI can
// confirm the wiring works even before the first real deploy.
//
// `eventType` is the value of GitHub's X-GitHub-Event header; passing "" is
// safe and simply leaves LastWebhookEvent blank.
func (s *ProjectService) HandleWebhook(ctx context.Context, projectID, sigHeader, eventType string, body []byte) error {
	// Decode the payload up-front so the repo-URL fallback (post-3.1.63)
	// has data to match on when projectID points at a row that no longer
	// exists on this box — the classic shape after a server transfer,
	// since the transfer pipeline mints fresh ObjectIDs for every
	// migrated project (idMap in transfer_panel_records.go). Without
	// the fallback, every GitHub-configured webhook on the source
	// breaks the moment cutover happens because the URL embeds the
	// SOURCE's _id; the user reported exactly this for the
	// MongoDB-Panel repo.
	var payload webhookPayload
	_ = json.Unmarshal(body, &payload) // best-effort; matches the pre-fix behaviour

	proj, oid, err := s.resolveProjectForWebhook(ctx, projectID, &payload.Repository)
	if err != nil {
		// Pre-3.1.73 this was silent — operator had no signal that GitHub
		// was hitting an unknown project_id (most often after a restore
		// from a different panel, or a stale webhook URL pointing at a
		// deleted project). Log so `journalctl -u serverpanel | grep
		// webhook` actually surfaces the symptom.
		log.Warn().
			Str("project_id_url", projectID).
			Str("repo_full_name", payload.Repository.FullName).
			Str("event", eventType).
			Msg("github webhook: project not found")
		return err
	}
	if proj.WebhookSecret == "" {
		// Should be unreachable now that GetWebhookSecret auto-heals on
		// read, but keep an explicit guard — VerifySignature returns
		// false on empty secret which is correct, but the operator
		// deserves a clearer diagnostic than "signature mismatch".
		s.stampWebhookError(ctx, oid, "no webhook secret set on project — open the project in WHM to auto-mint one")
		log.Warn().Str("project_id", oid.Hex()).Msg("github webhook: empty webhook_secret")
		return fmt.Errorf("webhook secret missing")
	}
	if !githubsig.VerifySignature(body, sigHeader, proj.WebhookSecret) {
		// 3.1.73 — stamp this as a *visible* error on the project so the
		// UI badge can show "Signature mismatch · 2m ago" instead of the
		// pre-fix "Waiting for first delivery". Most-common operator
		// mistake (#1 root cause of "auto-deploy not working" reports):
		// the secret pasted into GitHub doesn't match the secret on this
		// panel because of an invisible whitespace, an old secret left
		// over from before a Regenerate, or a copy that grabbed the
		// "•••" mask instead of the real value.
		s.stampWebhookError(ctx, oid, "signature mismatch — secret on GitHub doesn't match the panel; re-copy from the Webhook card")
		log.Warn().
			Str("project_id", oid.Hex()).
			Bool("has_signature_header", sigHeader != "").
			Int("body_bytes", len(body)).
			Msg("github webhook: signature mismatch")
		return fmt.Errorf("signature mismatch")
	}

	// Record the successful delivery before any early returns below. This is
	// the one piece of feedback that tells the operator "your webhook is
	// wired up correctly" — even for ping events or paused projects.
	// Also clear any prior LastWebhookError so a one-off mismatch from a
	// rotation doesn't haunt the badge after the operator fixes it.
	now := time.Now()
	s.db.Collection(database.ColProjects).UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set":   bson.M{"last_webhook_at": now, "last_webhook_event": eventType},
		"$unset": bson.M{"last_webhook_error": "", "last_webhook_error_at": ""},
	})

	if proj.Paused {
		log.Info().Str("project_id", oid.Hex()).Msg("github webhook: project paused, no-op")
		return nil
	}
	if !proj.AutoDeploy {
		log.Info().Str("project_id", oid.Hex()).Msg("github webhook: auto_deploy disabled, no-op")
		return nil
	}
	if eventType != "" && eventType != "push" {
		// GitHub was configured with "Send me everything" or a broader event
		// set — silently ignore anything that isn't a push, since we have no
		// reasonable action for issues / stars / pull requests / etc.
		log.Info().Str("project_id", oid.Hex()).Str("event", eventType).Msg("github webhook: non-push event, no-op")
		return nil
	}

	ref := payload.Ref
	if ref == "" {
		log.Info().Str("project_id", oid.Hex()).Msg("github webhook: empty ref (likely ping or tag-only), no-op")
		return nil
	}
	branch := strings.TrimPrefix(ref, "refs/heads/")

	services, _ := s.listServicesForProject(ctx, oid)

	// 3.1.85 — webhook means "pull and deploy all on-branch services",
	// full stop. The previous logic had three filters that silently
	// blocked deploys:
	//
	//   1. Strict branch equality. `svc.GitBranch != branch` skipped
	//      every legacy service that has GitBranch="" (because
	//      "" != "main"). We now treat empty as "track the project's
	//      branch" with a "main" fallback.
	//
	//   2. Per-service commit dedup. `svc.LastCommitSHA == payload.After`
	//      skipped on duplicate webhook delivery — but it also skipped
	//      legitimate re-pushes of the same SHA (force-push to same
	//      commit, post-revert re-deploy, manual re-trigger via
	//      "Recreate delivery" in the GitHub UI). Operators reported
	//      these as silent no-ops. The new policy: a verified webhook
	//      = a deploy intent, period.
	//
	//   3. Subpath matching. For monorepos with deep per-service
	//      git_subpath values, only services whose subpath was touched
	//      by the commit got deployed. This is a useful optimization in
	//      theory but a foot-gun in practice — operators iterating on
	//      shared libraries, root configs, or .env templates expected
	//      "I pushed → it deploys" and got "nothing to deploy" instead.
	//      The v3.1.72 cross-cutting fallback partially fixed this; now
	//      we drop the subpath gate entirely for the webhook path.
	//      Manual single-service deploy is still available via the
	//      Redeploy button in the WHM UI.
	//
	// Net behavior: a verified push to `main` deploys every service
	// whose GitBranch is `main` or empty (= follow project default).
	// The project-level pull still runs once via
	// runProjectPullAndEnqueue → inPlaceSync, so disk I/O is identical
	// to the smart-path flow. Services are enqueued with skipPull=true
	// to avoid N back-to-back pulls of the same shared clone.
	projectBranch := strings.TrimSpace(proj.GitBranch)
	if projectBranch == "" {
		projectBranch = "main"
	}
	type todoSvc struct {
		id  primitive.ObjectID
		svc models.ProjectService
	}
	var todo []todoSvc
	for _, svc := range services {
		svcBranch := strings.TrimSpace(svc.GitBranch)
		if svcBranch == "" {
			svcBranch = projectBranch
		}
		if svcBranch != branch {
			continue
		}
		todo = append(todo, todoSvc{id: svc.ID, svc: svc})
	}
	if len(todo) == 0 {
		// Only remaining no-op path: the push was to a branch no
		// service tracks (e.g. operator pushed to `develop` but every
		// service follows `main`). Surface clearly so the operator
		// can either fix the service's GitBranch or push to the
		// expected branch.
		log.Info().
			Str("project_id", oid.Hex()).
			Str("push_branch", branch).
			Str("project_branch", projectBranch).
			Str("commit_sha", payload.After).
			Int("services_total", len(services)).
			Msg("github webhook: no services track this branch, no-op")
		return nil
	}
	log.Info().
		Str("project_id", oid.Hex()).
		Str("branch", branch).
		Str("commit_sha", payload.After).
		Int("services_to_deploy", len(todo)).
		Msg("github webhook: pulling project + redeploying every on-branch service")
	// Pull + enqueue OFF the webhook request path (v3.1.63). Pre-fix
	// the project-level inPlaceSync ran synchronously here: a 5-second
	// `git pull` on a slow upstream blew past GitHub's 10-second
	// webhook timeout, GitHub closed the TCP connection mid-write
	// (nginx logs status 499), the delivery showed as "failed" in
	// the GitHub UI, and GitHub retried on a backoff curve — which
	// then queued a duplicate deploy when the retry succeeded. The
	// App webhook path (handlers/webhook_handler.go) already
	// fire-and-forgets the redeploy for the same reason; this brings
	// the project path into line.
	//
	// We DO want the operator to see "pull in progress" feedback in
	// the WHM UI rather than just an opaque "running", so the
	// background goroutine reuses the same inPlaceSync the
	// synchronous path used to call — it stamps last_commit_sha on
	// every service row when the pull succeeds, and runDeploy picks
	// up the new commit from there. Errors from inPlaceSync fall
	// through to per-service pulls inside runDeploy, where they're
	// captured in the deployment record's ErrorMsg the same way
	// they used to be when the project-level pull failed.
	todoIDs := make([]primitive.ObjectID, len(todo))
	for i, t := range todo {
		todoIDs[i] = t.id
	}
	go s.runProjectPullAndEnqueue(proj, oid, branch, "webhook", todoIDs)
	return nil
}

// resolveProjectForWebhook returns the project addressed by the webhook
// request, honouring the post-migration fallback path. Tries the
// route's :project_id first (every install before 3.1.63 only ever
// supported this shape); if no row matches AND the payload's
// repository.clone_url / ssh_url / html_url / full_name uniquely
// identifies a single project on this box, returns that one instead.
//
// Refuses the fallback when multiple projects share the same repo URL
// (rare — operator runs the same git source for two distinct deploy
// targets — but possible). Returning the wrong project would deploy
// the wrong code, so we'd rather fail closed with a clear error than
// silently pick one.
//
// The fallback is the missing piece that lets a GitHub-configured
// webhook URL survive a server-to-server migration: the URL embeds
// the source's project _id which the destination doesn't recognise
// (transfer remints ObjectIDs via idMap), but the payload's
// repository.* fields are identical regardless of which panel
// receives the delivery, so we can map source-id → destination-row
// by git_repo_url.
func (s *ProjectService) resolveProjectForWebhook(ctx context.Context, projectID string, repo *webhookRepository) (*models.Project, primitive.ObjectID, error) {
	col := s.db.Collection(database.ColProjects)

	// 1. Exact-id lookup. Same shape as pre-3.1.63.
	if oid, err := primitive.ObjectIDFromHex(projectID); err == nil {
		if proj, lerr := s.loadProject(ctx, oid); lerr == nil {
			return proj, oid, nil
		}
	}

	// 2. Repo-URL fallback. Build a candidate set from every
	//    "looks like the same repo" shape GitHub puts in the payload,
	//    canonicalised: lower-case, strip .git suffix, accept
	//    https://github.com/<owner>/<repo> AND git@github.com:<owner>/<repo>.
	if repo == nil {
		return nil, primitive.NilObjectID, fmt.Errorf("project not found")
	}
	candidates := canonicaliseRepoURLs(repo.CloneURL, repo.SSHURL, repo.HTMLURL, repo.FullName)
	if len(candidates) == 0 {
		return nil, primitive.NilObjectID, fmt.Errorf("project not found")
	}

	// Pull every row that mentions ANY candidate string. Cheap: there
	// are typically a couple dozen projects per install, and we only
	// need git_repo_url to make a decision.
	cur, err := col.Find(ctx, bson.M{})
	if err != nil {
		return nil, primitive.NilObjectID, fmt.Errorf("project not found")
	}
	defer cur.Close(ctx)
	var matches []models.Project
	for cur.Next(ctx) {
		var p models.Project
		if cur.Decode(&p) != nil {
			continue
		}
		canon := canonicaliseRepoURLs(p.GitRepoURL)
		if len(canon) == 0 {
			continue
		}
		row := canon[0]
		for _, want := range candidates {
			if row == want {
				matches = append(matches, p)
				break
			}
		}
	}
	if len(matches) == 0 {
		return nil, primitive.NilObjectID, fmt.Errorf("project not found")
	}
	if len(matches) > 1 {
		// Multiple deploy targets share this repo — we can't pick safely.
		// Operator must either update the GitHub webhook URL to use the
		// destination's project id, or delete the duplicate projects.
		return nil, primitive.NilObjectID, fmt.Errorf("project not found (repo %s mapped to %d projects, can't disambiguate)", repo.FullName, len(matches))
	}
	proj := &matches[0]
	return proj, proj.ID, nil
}

// canonicaliseRepoURLs reduces every "shape" of a GitHub repo URL to
// a single canonical form so the fallback lookup can compare across
// the panel's stored git_repo_url and GitHub's payload values.
// Returns one canonical string per non-empty input. Idempotent:
// passing an already-canonical form is a no-op.
//
// Accepts:
//   - https://github.com/owner/repo
//   - https://github.com/owner/repo.git
//   - https://<token>@github.com/owner/repo
//   - git@github.com:owner/repo.git
//   - owner/repo (the "full_name" shape GitHub emits in webhook payloads)
//
// Emits: lowercase "github.com/owner/repo" (no scheme, no .git, no
// credentials embedded in the host). Host is preserved so a self-
// hosted GitLab / Gitea install still uniquely identifies its repos.
func canonicaliseRepoURLs(urls ...string) []string {
	out := make([]string, 0, len(urls))
	seen := make(map[string]struct{})
	for _, raw := range urls {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		v = strings.ToLower(v)
		v = strings.TrimSuffix(v, ".git")
		// strip ssh prefix
		if strings.HasPrefix(v, "git@") {
			v = strings.TrimPrefix(v, "git@")
			v = strings.Replace(v, ":", "/", 1) // host:owner/repo → host/owner/repo
		}
		// strip http(s)
		v = strings.TrimPrefix(v, "https://")
		v = strings.TrimPrefix(v, "http://")
		// strip embedded credentials (token@host)
		if at := strings.Index(v, "@"); at >= 0 {
			if slash := strings.Index(v, "/"); slash < 0 || at < slash {
				v = v[at+1:]
			}
		}
		v = strings.TrimSuffix(v, "/")
		// full_name shape ("owner/repo" with no host) — leave as-is.
		// host/owner/repo shape — keep.
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// runProjectPullAndEnqueue runs the project-level git pull and then
// enqueues the affected services for deploy — moved off the webhook
// request path in v3.1.63 because the pull was timing GitHub out
// (10-second cap). Captures its own context so request cancellation
// on the inbound webhook doesn't kill the pull halfway through.
//
// Shared with the manual "Deploy all" UI button as of v3.1.68 — same
// one-pull-then-N-rebuilds shape so a 7-service project doesn't pay
// for 7 identical git fetches against the same shared clone (the
// observed wall-clock blow-up was ~20-90 s per extra-pull at scale,
// AND a window where a mid-deploy push could land different services
// on different commits). `trigger` lets the caller stamp "webhook" vs
// "manual" on the deployment row.
func (s *ProjectService) runProjectPullAndEnqueue(proj *models.Project, oid primitive.ObjectID, branch, trigger string, svcIDs []primitive.ObjectID) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	skipPull := false
	projectPullCommit := ""
	if proj.ProjectDir != "" && proj.GitRepoURL != "" {
		token, _ := s.decryptPAT(proj)
		remoteURL := proj.GitRepoURL
		if token != "" && strings.HasPrefix(remoteURL, "https://") {
			rest := remoteURL[len("https://"):]
			if at := strings.Index(rest, "@"); at >= 0 && at < strings.Index(rest, "/") {
				rest = rest[at+1:]
			}
			remoteURL = "https://" + token + "@" + rest
		}
		syncErr, syncOut, head := s.inPlaceSync(ctx, proj.ProjectDir, remoteURL, branch, token, proj.User)
		if syncErr == nil {
			skipPull = true
			projectPullCommit = strings.TrimSpace(head)
			if projectPullCommit != "" {
				s.db.Collection(database.ColProjectServices).UpdateMany(ctx, bson.M{"project_id": oid}, bson.M{
					"$set": bson.M{"last_commit_sha": projectPullCommit, "updated_at": time.Now()},
				})
			}
		} else {
			// Log at warn so operators see why the shared-pull
			// optimization didn't kick in (per-service pulls then
			// take over and the deploy still succeeds, just slower).
			snippet := syncOut
			if len(snippet) > 240 {
				snippet = "…" + snippet[len(snippet)-240:]
			}
			log.Warn().
				Err(syncErr).
				Str("project", proj.Slug).
				Str("project_dir", proj.ProjectDir).
				Str("branch", branch).
				Str("trail", snippet).
				Msg("project-level inPlaceSync failed; falling back to per-service pulls")
		}
	}
	if trigger == "" {
		trigger = "manual"
	}
	for _, id := range svcIDs {
		s.enqueueWithCommit(id, trigger, skipPull, projectPullCommit)
	}
}

// enqueueWithCommit is the project-pull-aware sibling of enqueue —
// threads the shared HEAD sha through to runDeploy so the per-service
// "Pull source from Git" step can render as COMPLETED (with the SHA)
// instead of "skipped" when a project-level pull actually ran. The
// thin enqueue() shim above keeps callers that don't need the commit
// (per-service Redeploy button) unchanged.
func (s *ProjectService) enqueueWithCommit(svcID primitive.ObjectID, trigger string, skipPull bool, projectPullCommit string) {
	select {
	case s.deployQueue <- deployJob{serviceID: svcID, trigger: trigger, skipPull: skipPull, projectPullCommit: projectPullCommit}:
	default:
		s.db.Collection(database.ColProjectServices).UpdateOne(
			context.Background(),
			bson.M{"_id": svcID},
			bson.M{"$set": bson.M{"status": "queue-full"}},
		)
	}
}

// webhookPayload is a minimal subset of GitHub's push event payload.
// Repository fields are used by the post-3.1.63 fallback path: when the
// route's :project_id doesn't match any row on this box (the classic
// post-server-migration shape, since transfer re-mints every project's
// ObjectID) we look up by Repository.CloneURL / Repository.SSHURL so
// GitHub deliveries to the SOURCE's URL keep working unchanged.
type webhookPayload struct {
	Ref        string             `json:"ref"`
	After      string             `json:"after"`
	Commits    []webhookCommit    `json:"commits"`
	Repository webhookRepository  `json:"repository"`
}
type webhookCommit struct {
	Added    []string `json:"added"`
	Modified []string `json:"modified"`
	Removed  []string `json:"removed"`
}
type webhookRepository struct {
	FullName string `json:"full_name"` // e.g. "BetaZen-InfoTech/MongoDB-Panel"
	CloneURL string `json:"clone_url"` // e.g. "https://github.com/BetaZen-InfoTech/MongoDB-Panel.git"
	SSHURL   string `json:"ssh_url"`   // e.g. "git@github.com:BetaZen-InfoTech/MongoDB-Panel.git"
	HTMLURL  string `json:"html_url"`  // e.g. "https://github.com/BetaZen-InfoTech/MongoDB-Panel"
}

// GetDeploymentLogs returns the tail of a service's last-N deployment logs,
// newest first. Reads the on-disk log file path stored on the deployment
// record. Empty list when no deployments have run yet.
func (s *ProjectService) GetDeploymentLogs(ctx context.Context, svcID string, limit int) ([]ProjectLogEntry, error) {
	oid, err := primitive.ObjectIDFromHex(svcID)
	if err != nil {
		return nil, fmt.Errorf("invalid service id")
	}
	if limit <= 0 {
		limit = 5
	}
	cur, err := s.db.Collection(database.ColProjectDeployments).Find(ctx, bson.M{"service_id": oid},
		options.Find().SetSort(bson.D{{Key: "started_at", Value: -1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var deps []models.ProjectDeployment
	if err := cur.All(ctx, &deps); err != nil {
		return nil, err
	}
	out := make([]ProjectLogEntry, 0, len(deps))
	for _, d := range deps {
		entry := ProjectLogEntry{
			DeploymentID: d.ID.Hex(),
			Trigger:      d.Trigger,
			Status:       d.Status,
			StartedAt:    d.StartedAt,
			FinishedAt:   d.FinishedAt,
			Commit:       d.CommitSHA,
			Error:        d.ErrorMsg,
		}
		if d.LogPath != "" {
			if b, err := os.ReadFile(d.LogPath); err == nil {
				entry.Output = string(b)
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// ProjectLogEntry is the shape returned to the UI — flatter and more
// UI-friendly than the raw ProjectDeployment document.
type ProjectLogEntry struct {
	DeploymentID string     `json:"deployment_id"`
	Trigger      string     `json:"trigger"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	Commit       string     `json:"commit"`
	Error        string     `json:"error"`
	Output       string     `json:"output"`
}
