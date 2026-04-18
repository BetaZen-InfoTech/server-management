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
			return nil, fmt.Errorf("service %q: %w", req.Services[i].Name, err)
		}
		services = append(services, *svc)
	}
	return &ProvisionResult{Project: proj, Services: services}, nil
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
	if err := validateServiceName(req.Name); err != nil {
		return nil, err
	}
	if req.User == "" {
		req.User = defaultProjectUser(proj.Slug)
	}
	if req.GitBranch == "" {
		req.GitBranch = "main"
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

	// --- Clone code into installDir --------------------------------------
	token, err := s.decryptPAT(proj)
	if err != nil {
		return nil, err
	}
	tmp := installDir + ".src"
	agent.RunCommand(ctx, "rm", "-rf", tmp)
	if err := agent.GitClone(ctx, req.GitRepoURL, req.GitBranch, tmp, token); err != nil {
		return nil, fmt.Errorf("git clone failed: %w", err)
	}
	// If GitSubpath is set, only move that subdirectory into installDir.
	src := tmp
	if sub := strings.Trim(strings.TrimSpace(req.GitSubpath), "/"); sub != "" {
		src = filepath.Join(tmp, sub)
	}
	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("shopt -s dotglob && mv %s/* %s/ && rm -rf %s", src, installDir, tmp)); err != nil {
		return nil, fmt.Errorf("stage checkout: %w", err)
	}
	if err := chownRecursive(ctx, installDir, req.User); err != nil {
		return nil, err
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
		if err := runBuildAsUser(ctx, req.User, installDir, req.InstallCmd, runtimeBinDir); err != nil {
			return nil, fmt.Errorf("install step %w", err)
		}
	}
	if req.BuildCmd != "" {
		if err := runBuildAsUser(ctx, req.User, installDir, req.BuildCmd, runtimeBinDir); err != nil {
			return nil, fmt.Errorf("build step %w", err)
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
		if err := agent.CreateSystemdUnit(ctx, unitName, req.User, installDir, startCmd, env); err != nil {
			return nil, fmt.Errorf("create systemd unit: %w", err)
		}
		waitForPort(req.Port, 6*time.Second)
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
		Status:        "running",
		LastDeployedAt: &now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	res, err := s.db.Collection(database.ColProjectServices).InsertOne(ctx, svc)
	if err != nil {
		return nil, err
	}
	svc.ID = res.InsertedID.(primitive.ObjectID)
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
		_ = os.RemoveAll(svc.InstallDir)
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
				agent.DeleteVhost(ctx, svc.PrimaryDomain)
			}
		} else {
			agent.DeleteVhost(ctx, svc.PrimaryDomain)
		}
	}
	return nil
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
func (s *ProjectService) startWorker() {
	for job := range s.deployQueue {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		s.runDeploy(ctx, job)
		cancel()
	}
}

// runDeploy is the actual work of re-deploying a single service: git pull,
// install, build, restart. Status + log are written back on every boundary so
// the UI's "deploying" indicator is accurate.
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

	dep := models.ProjectDeployment{
		ProjectID: proj.ID,
		ServiceID: svc.ID,
		Trigger:   job.trigger,
		Status:    "running",
		StartedAt: time.Now(),
		LogPath:   logPath,
	}
	res, _ := s.db.Collection(database.ColProjectDeployments).InsertOne(ctx, dep)
	depID, _ := res.InsertedID.(primitive.ObjectID)

	finalize := func(status, errMsg string, commit string) {
		now := time.Now()
		update := bson.M{
			"status":      status,
			"finished_at": now,
			"error_msg":   errMsg,
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
	if result, err := agent.RunCommand(ctx, "git", "-C", svc.InstallDir, "pull", "origin", svc.GitBranch); err != nil {
		appendLog(logPath, "git pull failed: "+err.Error())
		if result != nil {
			appendLog(logPath, result.Output+"\n"+result.Error)
		}
		finalize("error", "git pull failed", "")
		return
	}

	commit := ""
	if res, err := agent.RunCommand(ctx, "git", "-C", svc.InstallDir, "rev-parse", "HEAD"); err == nil {
		commit = strings.TrimSpace(res.Output)
	}

	runtimeBinDir := resolveRuntimeBinDir(roleToAppType(svc.Role), "")
	if svc.InstallCmd != "" {
		if err := runBuildAsUser(ctx, svc.User, svc.InstallDir, svc.InstallCmd, runtimeBinDir); err != nil {
			appendLog(logPath, "install: "+err.Error())
			finalize("error", "install failed", commit)
			return
		}
	}
	if svc.BuildCmd != "" {
		if err := runBuildAsUser(ctx, svc.User, svc.InstallDir, svc.BuildCmd, runtimeBinDir); err != nil {
			appendLog(logPath, "build: "+err.Error())
			finalize("error", "build failed", commit)
			return
		}
	}
	if svc.Role == "backend" && svc.SystemdUnit != "" {
		agent.RunCommand(ctx, "systemctl", "restart", svc.SystemdUnit)
		waitForPort(svc.Port, 6*time.Second)
	}
	finalize("running", "", commit)
}

// HandleWebhook verifies the GitHub HMAC signature then enqueues redeploys
// for every service whose GitSubpath matches a path in the commit's
// added/modified/removed list. A missing or empty `commits` field (e.g. a
// ping event) triggers nothing — only push events redeploy.
func (s *ProjectService) HandleWebhook(ctx context.Context, projectID, sigHeader string, body []byte) error {
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
	if proj.Paused || !proj.AutoDeploy {
		return nil // accepted but no-op
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
