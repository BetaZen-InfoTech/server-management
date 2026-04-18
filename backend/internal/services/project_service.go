package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/crypto"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/githubsig"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

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
}

type deployJob struct {
	serviceID primitive.ObjectID
	trigger   string
}

// NewProjectService wires up the dependencies and starts the deploy worker
// pool. The worker count is a deliberate low number — concurrent `npm install`
// runs on a small VPS saturate CPU + IO fast, and deploys are fundamentally
// serial from the operator's mental model anyway.
func NewProjectService(db *mongo.Database, encKey []byte, webhookBaseURL, sslEmail, serverIP string) *ProjectService {
	s := &ProjectService{
		db:             db,
		encKey:         encKey,
		webhookBaseURL: strings.TrimRight(webhookBaseURL, "/"),
		sslEmail:       sslEmail,
		serverIP:       serverIP,
		deployQueue:    make(chan deployJob, 64),
	}
	for i := 0; i < 2; i++ {
		go s.startWorker()
	}
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

	proj := models.Project{
		Name:          strings.TrimSpace(req.Name),
		Description:   req.Description,
		WebhookSecret: secret,
		AutoDeploy:    req.AutoDeploy,
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
	proj, err := s.Create(ctx, &models.CreateProjectRequest{
		Name:        req.Name,
		Description: req.Description,
		GitHubPAT:   req.GitHubPAT,
		AutoDeploy:  req.AutoDeploy,
	})
	if err != nil {
		return nil, err
	}
	services := make([]models.ProjectService, 0, len(req.Services))
	for i := range req.Services {
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

// List returns every project the caller has access to, paged. Tenant-scoped
// users only see their own tenant's projects.
func (s *ProjectService) List(ctx context.Context, page, limit int) ([]models.Project, int64, error) {
	col := s.db.Collection(database.ColProjects)
	filter := bson.M{}
	if scope := GetCallerScope(ctx); scope != nil && scope.TenantHex != "" {
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
	svcs, _ := s.listServicesForProject(ctx, oid)
	for _, svc := range svcs {
		_ = s.removeServiceInternal(ctx, &svc)
	}
	_, _ = s.db.Collection(database.ColProjectServices).DeleteMany(ctx, bson.M{"project_id": oid})
	_, _ = s.db.Collection(database.ColProjectDeployments).DeleteMany(ctx, bson.M{"project_id": oid})
	_, err = s.db.Collection(database.ColProjects).DeleteOne(ctx, bson.M{"_id": oid})
	return err
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
func (s *ProjectService) GetWebhookSecret(ctx context.Context, id string) (string, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return "", fmt.Errorf("invalid project id")
	}
	var p models.Project
	if err := s.db.Collection(database.ColProjects).FindOne(ctx, bson.M{"_id": oid}).Decode(&p); err != nil {
		return "", err
	}
	return p.WebhookSecret, nil
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
func (s *ProjectService) loadProject(ctx context.Context, oid primitive.ObjectID) (*models.Project, error) {
	var p models.Project
	if err := s.db.Collection(database.ColProjects).FindOne(ctx, bson.M{"_id": oid}).Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
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
	if req.User == "" {
		// Prefer the owning user of the service's primary domain so the
		// project's source lands under that user's existing /home dir
		// instead of an auto-generated `sp-<slug>-<hash>` account.
		// Operators expect /home/<their-user>/projects/<project>/<svc>/,
		// not /home/sp-mongo-ba1c/projects/mongo/svc/.
		// Falls back to the auto-generated user only when:
		//   a) no primary_domain was set, OR
		//   b) the domain isn't registered in the panel (operator typed a
		//      foreign hostname that doesn't have a /home/<u>/domains/
		//      entry yet).
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

	installDir := fmt.Sprintf("/home/%s/projects/%s/%s", req.User, proj.Slug, req.Name)
	if err := prepareAppDir(ctx, installDir, req.User); err != nil {
		return nil, err
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
	// If GitSubpath is set, only move that subdirectory into installDir.
	// (Already validated above — cleanSubpath is filepath.Clean'd and known
	// not to escape the tempdir.)
	src := tmp
	if req.GitSubpath != "" {
		src = filepath.Join(tmp, req.GitSubpath)
	}
	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("shopt -s dotglob && mv %s/* %s/ && rm -rf %s", src, installDir, tmp)); err != nil {
		return nil, fmt.Errorf("stage checkout: %w", err)
	}
	if err := chownRecursive(ctx, installDir, req.User); err != nil {
		return nil, err
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
	missingEnvKeys := requiredEnvKeysFromExample(installDir, req.EnvVars)

	// --- Auto-detect from package.json -----------------------------------
	// Reads framework, port, install/build/start from the just-cloned source
	// and fills any fields the operator left blank. Everything the operator
	// did provide wins — detection never overrides explicit values.
	//
	// After detection, re-apply the matching framework preset to pick up
	// anything the package.json didn't specify (e.g. a Next.js repo with
	// only scripts.build — we still want the preset's start command).
	hints := DetectPackageJSONHints(installDir)
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
		writeFileAsUser(ctx, filepath.Join(installDir, ".env"), strings.Join(lines, "\n")+"\n", req.User, "0600")
	}

	runtimeBinDir := resolveRuntimeBinDir(roleToAppType(req.Role), "")
	if req.InstallCmd != "" {
		if err := runBuildAsUser(ctx, req.User, installDir, withNoColor(req.InstallCmd), runtimeBinDir); err != nil {
			return nil, buildErrorFrom("install", err)
		}
	}
	if req.BuildCmd != "" {
		if err := runBuildAsUser(ctx, req.User, installDir, withNoColor(req.BuildCmd), runtimeBinDir); err != nil {
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
		startCmd := renderStartCmd(req.StartCmd, req.Port)
		if strings.TrimSpace(startCmd) == "" {
			return nil, fmt.Errorf("start_cmd is required for backend services")
		}
		// Pre-flight: if the start command is a direct `node X.js`
		// invocation, make sure X.js actually exists in the cloned repo.
		// Otherwise systemd will crash-loop the unit forever and all
		// nginx ever sees is a dead upstream (= 502 Bad Gateway).
		if entry := extractNodeEntry(startCmd); entry != "" {
			entryPath := filepath.Join(installDir, entry)
			if _, statErr := os.Stat(entryPath); os.IsNotExist(statErr) {
				// Prefer the repo's own scripts.start if it pointed at a
				// different (and presumably correct) entry file. If that
				// also can't help, fail clean with a specific message.
				if hints.StartCmd != "" && hints.StartCmd != req.StartCmd {
					if hintEntry := extractNodeEntry(hints.StartCmd); hintEntry != "" {
						if _, e2 := os.Stat(filepath.Join(installDir, hintEntry)); e2 == nil {
							req.StartCmd = hints.StartCmd
							startCmd = renderStartCmd(req.StartCmd, req.Port)
							fmt.Fprintf(os.Stderr, "[project %s/%s] start_cmd switched to package.json scripts.start (%s was missing)\n", proj.Slug, req.Name, entry)
						}
					}
				}
				// Re-check in case the fallback above fixed it.
				if entry := extractNodeEntry(startCmd); entry != "" {
					if _, e := os.Stat(filepath.Join(installDir, entry)); os.IsNotExist(e) {
						return nil, &BuildError{
							Stage:   "start",
							Summary: fmt.Sprintf("start command references %q but that file isn't in the repo", entry),
							Details: fmt.Sprintf("Your start command is:\n  %s\n\nBut %s doesn't exist at the repo root (cloned into %s).\n\nFix one of:\n  1. Change start_cmd to point at your real entry file (e.g. node index.js / node dist/server.js)\n  2. Add a scripts.start to package.json that runs the correct entry\n  3. Move the subpath to the directory that contains the entry file", startCmd, entry, installDir),
						}
					}
				}
			}
		}
		if err := agent.CreateSystemdUnit(ctx, unitName, req.User, installDir, startCmd, env); err != nil {
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
	buildDir := installDir
	if req.Framework != "" {
		if p, ok := lookupPreset(req.Framework); ok && p.StaticDir != "" {
			buildDir = filepath.Join(installDir, p.StaticDir)
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
		InstallCmd:    req.InstallCmd,
		BuildCmd:      req.BuildCmd,
		StartCmd:      req.StartCmd,
		Port:          req.Port,
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
	if needsRestart && svc.Role == "backend" {
		agent.RunCommand(ctx, "systemctl", "restart", svc.SystemdUnit)
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
	if svc.InstallDir != "" {
		// Soft-delete: rename to <dir>.deleted-<ts> so the operator's
		// code is preserved and discoverable via File Manager. Fully
		// removing it on every Remove click was destroying source
		// without any way to recover. The systemd unit + nginx vhost
		// are gone, so the rename frees the namespace for a future
		// re-create with the same name without conflict.
		softDeleteDir(svc.InstallDir, "deleted")
	}
	if _, err := s.db.Collection(database.ColProjectServices).DeleteOne(ctx, bson.M{"_id": svc.ID}); err != nil {
		return err
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
				// Write a placeholder vhost instead of deleting outright.
				// Without this, browsing the domain falls through via SNI
				// to whatever other :443 server_name nginx loaded first,
				// which looks like "wrong site serving wrong cert" to
				// the operator. The placeholder keeps the domain's own
				// cert binding and serves a clean "site not deployed"
				// page until the operator reuses or removes the domain.
				_ = agent.WritePlaceholderVhost(ctx, svc.PrimaryDomain)
			}
		} else {
			_ = agent.WritePlaceholderVhost(ctx, svc.PrimaryDomain)
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
	if svc.SystemdUnit == "" || svc.Role != "backend" {
		return nil // no process to control
	}
	switch action {
	case "start", "stop", "restart":
		// ok
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	if _, err := agent.RunCommand(ctx, "systemctl", action, svc.SystemdUnit); err != nil {
		return fmt.Errorf("systemctl %s: %w", action, err)
	}
	newStatus := map[string]string{"start": "running", "stop": "stopped", "restart": "running"}[action]
	s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": svc.ID}, bson.M{
		"$set": bson.M{"status": newStatus, "updated_at": time.Now()},
	})
	return nil
}

// ProjectAction fan-outs a systemctl operation across every backend service
// in the project. Errors are accumulated so one broken service doesn't stop
// the rest from being acted on.
func (s *ProjectService) ProjectAction(ctx context.Context, projectID, action string) error {
	oid, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		return fmt.Errorf("invalid project id")
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

// AddAlias appends an alias domain to a service and re-issues the SSL cert so
// the new domain is included. Rejects duplicates.
func (s *ProjectService) AddAlias(ctx context.Context, svcID, domain string) (*models.ProjectService, error) {
	domain = sanitizeDomain(domain)
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	svc, err := s.GetService(ctx, svcID)
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
	aliases := append([]string{}, svc.AliasDomains...)
	aliases = append(aliases, domain)

	proj, err := s.loadProject(ctx, svc.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.reconcileVhostFor(ctx, proj, svc.Role, svc.PrimaryDomain, aliases, svc.PathPrefix, svc.Port, svc.BuildDir); err != nil {
		return nil, err
	}
	_, err = s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": svc.ID}, bson.M{
		"$set": bson.M{"alias_domains": aliases, "updated_at": time.Now()},
	})
	if err != nil {
		return nil, err
	}
	return s.GetService(ctx, svcID)
}

// RemoveAlias deletes an alias. The cert is NOT shrunk (that would need
// certbot delete + re-issue and risks downtime); it simply stops being served
// because nginx no longer lists the alias in server_name.
func (s *ProjectService) RemoveAlias(ctx context.Context, svcID, domain string) (*models.ProjectService, error) {
	domain = sanitizeDomain(domain)
	svc, err := s.GetService(ctx, svcID)
	if err != nil {
		return nil, err
	}
	kept := make([]string, 0, len(svc.AliasDomains))
	for _, a := range svc.AliasDomains {
		if a != domain {
			kept = append(kept, a)
		}
	}
	proj, err := s.loadProject(ctx, svc.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.reconcileVhostFor(ctx, proj, svc.Role, svc.PrimaryDomain, kept, svc.PathPrefix, svc.Port, svc.BuildDir); err != nil {
		return nil, err
	}
	_, err = s.db.Collection(database.ColProjectServices).UpdateOne(ctx, bson.M{"_id": svc.ID}, bson.M{
		"$set": bson.M{"alias_domains": kept, "updated_at": time.Now()},
	})
	if err != nil {
		return nil, err
	}
	return s.GetService(ctx, svcID)
}

// DeployAll enqueues every service in a project for redeploy. Returns
// immediately; the worker pool processes them one at a time.
func (s *ProjectService) DeployAll(ctx context.Context, projectID string) error {
	oid, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		return fmt.Errorf("invalid project id")
	}
	svcs, err := s.listServicesForProject(ctx, oid)
	if err != nil {
		return err
	}
	for _, svc := range svcs {
		s.enqueue(svc.ID, "manual")
	}
	return nil
}

// DeployService enqueues a single service for redeploy.
func (s *ProjectService) DeployService(svcID string, trigger string) error {
	oid, err := primitive.ObjectIDFromHex(svcID)
	if err != nil {
		return fmt.Errorf("invalid service id")
	}
	s.enqueue(oid, trigger)
	return nil
}

func (s *ProjectService) enqueue(svcID primitive.ObjectID, trigger string) {
	select {
	case s.deployQueue <- deployJob{serviceID: svcID, trigger: trigger}:
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
		svcUpdate := bson.M{"status": status, "updated_at": now, "last_deployed_at": now}
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
	agent.RunCommand(ctx, "git", "-C", svc.InstallDir, "remote", "set-url", "origin", remoteURL)
	// --- Step 0: pull source ---
	startStep(0)
	// 5-minute timeout on the pull so a hung connection doesn't starve the
	// worker's 15-minute total budget. Scrub the token out of any error
	// output before writing to the log (git sometimes echoes the full URL).
	pullCtx, pullCancel := context.WithTimeout(ctx, 5*time.Minute)
	pullResult, pullErr := agent.RunCommand(pullCtx, "git", "-C", svc.InstallDir, "pull", "origin", svc.GitBranch)
	pullCancel()
	if pullErr != nil {
		appendLog(logPath, "git pull failed: "+sanitiseGitError(pullErr, token))
		if pullResult != nil {
			safeOutput := pullResult.Output
			safeErr := pullResult.Error
			if token != "" {
				safeOutput = strings.ReplaceAll(safeOutput, token, "***")
				safeErr = strings.ReplaceAll(safeErr, token, "***")
			}
			appendLog(logPath, safeOutput+"\n"+safeErr)
		}
		failStep(0, "git pull failed: "+sanitiseGitError(pullErr, token))
		finalize("error", "git pull failed", "")
		return
	}

	commit := ""
	if res, err := agent.RunCommand(ctx, "git", "-C", svc.InstallDir, "rev-parse", "HEAD"); err == nil {
		commit = strings.TrimSpace(res.Output)
	}
	pullDetails := "Pulled latest from " + svc.GitBranch
	if commit != "" {
		shortSHA := commit
		if len(shortSHA) > 7 {
			shortSHA = shortSHA[:7]
		}
		pullDetails += " (" + shortSHA + ")"
	}
	completeStep(0, pullDetails)

	runtimeBinDir := resolveRuntimeBinDir(roleToAppType(svc.Role), "")
	// --- Step 1: install dependencies ---
	if svc.InstallCmd != "" {
		startStep(1)
		if err := runBuildAsUser(ctx, svc.User, svc.InstallDir, withNoColor(svc.InstallCmd), runtimeBinDir); err != nil {
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
		if err := runBuildAsUser(ctx, svc.User, svc.InstallDir, withNoColor(svc.BuildCmd), runtimeBinDir); err != nil {
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
			completeStep(4, fmt.Sprintf("Restarted; port :%d not bound yet (will retry)", svc.Port))
		}
	} else {
		skipStep(3, "static service — no systemd unit to restart")
		skipStep(4, "static service — no port to health-check")
	}
	finalize("running", "", commit)
}

// lookupDomainOwner returns the system user that owns a registered domain,
// or empty string when the domain isn't in the panel's domains collection.
// Used by Provision/AddService to default the service's User to the same
// account that owns the primary_domain — keeps the project files under
// /home/<existing-user>/ instead of spawning a throwaway sp-<slug>-<hash>
// account just because the wizard didn't ask for a user explicitly.
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
	oid, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		return fmt.Errorf("invalid project id")
	}
	proj, err := s.loadProject(ctx, oid)
	if err != nil {
		return fmt.Errorf("project not found")
	}
	if !githubsig.VerifySignature(body, sigHeader, proj.WebhookSecret) {
		return fmt.Errorf("signature mismatch")
	}

	// Record the successful delivery before any early returns below. This is
	// the one piece of feedback that tells the operator "your webhook is
	// wired up correctly" — even for ping events or paused projects.
	now := time.Now()
	s.db.Collection(database.ColProjects).UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{"last_webhook_at": now, "last_webhook_event": eventType},
	})

	if proj.Paused || !proj.AutoDeploy {
		return nil // accepted but no-op
	}
	if eventType != "" && eventType != "push" {
		// GitHub was configured with "Send me everything" or a broader event
		// set — silently ignore anything that isn't a push, since we have no
		// reasonable action for issues / stars / pull requests / etc.
		return nil
	}

	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil // malformed is not our problem — GitHub won't retry 200s
	}
	ref := payload.Ref
	if ref == "" {
		return nil
	}
	branch := strings.TrimPrefix(ref, "refs/heads/")

	services, _ := s.listServicesForProject(ctx, oid)
	// Collect all changed paths from every commit in the payload.
	changed := map[string]struct{}{}
	for _, c := range payload.Commits {
		for _, p := range c.Added {
			changed[p] = struct{}{}
		}
		for _, p := range c.Modified {
			changed[p] = struct{}{}
		}
		for _, p := range c.Removed {
			changed[p] = struct{}{}
		}
	}
	for _, svc := range services {
		if svc.GitBranch != branch {
			continue
		}
		sub := strings.Trim(svc.GitSubpath, "/")
		affected := false
		if sub == "" {
			affected = true // repo root maps to this service
		} else {
			for p := range changed {
				if strings.HasPrefix(p, sub+"/") || p == sub {
					affected = true
					break
				}
			}
		}
		if !affected {
			continue
		}
		// Skip if we're already on this commit (duplicate delivery or out-of-order).
		if payload.After != "" && svc.LastCommitSHA == payload.After {
			continue
		}
		s.enqueue(svc.ID, "webhook")
	}
	return nil
}

// webhookPayload is a minimal subset of GitHub's push event payload.
type webhookPayload struct {
	Ref     string           `json:"ref"`
	After   string           `json:"after"`
	Commits []webhookCommit  `json:"commits"`
}
type webhookCommit struct {
	Added    []string `json:"added"`
	Modified []string `json:"modified"`
	Removed  []string `json:"removed"`
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
