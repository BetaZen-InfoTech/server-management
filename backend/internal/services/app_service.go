package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type AppService struct {
	db *mongo.Database
}

func NewAppService(db *mongo.Database) *AppService {
	return &AppService{db: db}
}

func (s *AppService) List(ctx context.Context, page, limit int) ([]models.App, int64, error) {
	col := s.db.Collection(database.ColApps)
	filter := bson.M{}
	if scope := GetCallerScope(ctx); scope != nil {
		filter = scope.ApplyTo(ctx, s.db, "user", filter)
	}

	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().SetSkip(skip).SetLimit(int64(limit)).SetSort(bson.D{{Key: "created_at", Value: -1}})
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var apps []models.App
	if err := cursor.All(ctx, &apps); err != nil {
		return nil, 0, err
	}

	// Check live status for each app
	for i := range apps {
		serviceName := "sp-app-" + apps[i].Name
		result, err := agent.RunCommand(ctx, "systemctl", "is-active", serviceName)
		if err == nil && strings.TrimSpace(result.Output) == "active" {
			apps[i].Status = "running"
		} else if apps[i].Status != "deploying" {
			apps[i].Status = "stopped"
		}
	}

	if apps == nil {
		apps = []models.App{}
	}
	return apps, total, nil
}

func (s *AppService) GetByName(ctx context.Context, name string) (*models.App, error) {
	col := s.db.Collection(database.ColApps)
	var app models.App
	if err := col.FindOne(ctx, bson.M{"name": name}).Decode(&app); err != nil {
		return nil, err
	}

	// Check live status
	serviceName := "sp-app-" + app.Name
	result, err := agent.RunCommand(ctx, "systemctl", "is-active", serviceName)
	if err == nil && strings.TrimSpace(result.Output) == "active" {
		app.Status = "running"
	} else if app.Status != "deploying" {
		app.Status = "stopped"
	}

	return &app, nil
}

func (s *AppService) Deploy(ctx context.Context, req *models.DeployAppRequest) (*models.App, error) {
	// --- 1. Validation & normalization -----------------------------------
	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	req.User = strings.TrimSpace(req.User)
	req.Domain = sanitizeDomain(req.Domain)
	req.AppType = normalizeAppType(req.AppType)

	if err := validateAppName(req.Name); err != nil {
		return nil, err
	}

	// Interpreted / build-step runtimes need an install/build step before
	// they can start (npm install, pip install, bundle install, go build,
	// ...). Reject deploys that leave build_cmd empty for these types so
	// operators don't end up with a broken service that ExecStart can't
	// find. Presets set a default below, so this only fires for
	// deploy_method=local / git without a framework selected.
	if strings.TrimSpace(req.BuildCmd) == "" && req.Framework == "" {
		if hint, ok := missingBuildCmdHint(req.AppType); ok {
			return nil, fmt.Errorf("%s apps require a build command (e.g. %q) — set one below, or pick a Framework preset to auto-fill it", req.AppType, hint)
		}
	}

	col := s.db.Collection(database.ColApps)

	// Duplicate check.
	if err := col.FindOne(ctx, bson.M{"name": req.Name}).Err(); err == nil {
		return nil, fmt.Errorf("app %q already exists; pick another name or delete the existing one", req.Name)
	}

	// Track whether the caller wanted automatic port allocation. A preset's
	// DefaultPort is a suggestion, not a hard assignment — if it collides
	// with an already-deployed app, allocate a free port instead.
	autoPort := req.Port == 0

	// --- 2. Framework preset ---------------------------------------------
	var preset Preset
	hasPreset := false
	if req.Framework != "" {
		if p, ok := lookupPreset(req.Framework); ok {
			preset = p
			hasPreset = true
			if req.AppType == "" {
				req.AppType = p.AppType
			}
			if req.BuildCmd == "" {
				req.BuildCmd = p.BuildCmd
			}
			if req.StartCmd == "" {
				req.StartCmd = p.StartCmd
			}
			if autoPort && p.DefaultPort > 0 {
				req.Port = p.DefaultPort
			}
		}
	}
	if req.AppType == "" {
		req.AppType = "node"
	}

	isStatic := req.AppType == "static" || (hasPreset && preset.IsStatic)

	// --- 3. Port allocation ----------------------------------------------
	if !isStatic {
		// Collect ports already used by deployed apps.
		used := map[int]bool{}
		cur, _ := col.Find(ctx, bson.M{}, options.Find().SetProjection(bson.M{"port": 1}))
		if cur != nil {
			var rows []struct {
				Port int `bson:"port"`
			}
			_ = cur.All(ctx, &rows)
			for _, r := range rows {
				if r.Port > 0 {
					used[r.Port] = true
				}
			}
		}

		if autoPort {
			// Preset suggested a default; honor it only if it's actually free.
			if req.Port > 0 && !used[req.Port] && isPortFree(req.Port) {
				// keep preset default
			} else {
				p, err := allocatePort(used)
				if err != nil {
					return nil, fmt.Errorf("could not allocate free port: %w", err)
				}
				req.Port = p
			}
		} else {
			if used[req.Port] || !isPortFree(req.Port) {
				return nil, fmt.Errorf("port %d is already in use on the server", req.Port)
			}
		}
	}

	// --- 4. Ensure system user exists ------------------------------------
	if err := ensureUser(ctx, req.User); err != nil {
		return nil, err
	}

	// --- 5. Prepare app directory (clean + chown) ------------------------
	appDir := fmt.Sprintf("/home/%s/apps/%s", req.User, req.Name)
	if err := prepareAppDir(ctx, appDir, req.User); err != nil {
		return nil, err
	}

	// --- 6. Fetch source code --------------------------------------------
	switch req.DeployMethod {
	case "git":
		if req.GitURL == "" {
			return nil, fmt.Errorf("deploy_method=git requires a non-empty git_url")
		}
		branch := req.GitBranch
		if branch == "" {
			branch = "main"
		}
		// Clone into a temp subdir so clean existing dir stays empty for git clone.
		tmp := appDir + ".src"
		agent.RunCommand(ctx, "rm", "-rf", tmp)
		if err := agent.GitClone(ctx, req.GitURL, branch, tmp, req.GitToken); err != nil {
			return nil, fmt.Errorf("git clone failed: %w", err)
		}
		// Move contents into appDir.
		if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("shopt -s dotglob && mv %s/* %s/ && rmdir %s", tmp, appDir, tmp)); err != nil {
			return nil, fmt.Errorf("failed to stage git checkout: %w", err)
		}
	case "scaffold":
		if !hasPreset {
			return nil, fmt.Errorf("deploy_method=scaffold requires a known 'framework' value")
		}
		for relPath, content := range preset.Scaffold {
			full := filepath.Join(appDir, relPath)
			if err := writeFileAsUser(ctx, full, content, req.User, ""); err != nil {
				return nil, fmt.Errorf("scaffold %s: %w", relPath, err)
			}
		}
	case "local":
		// Caller has already placed files at appDir; nothing to fetch.
	case "zip", "binary", "docker":
		// Not yet implemented — accept the request but warn in logs.
	}

	// Ensure everything in appDir belongs to the app user (git clone / mv / etc.)
	if err := chownRecursive(ctx, appDir, req.User); err != nil {
		return nil, fmt.Errorf("chown %s: %w", appDir, err)
	}

	// --- 7. Env file ------------------------------------------------------
	if len(req.EnvVars) > 0 {
		var envLines []string
		for k, v := range req.EnvVars {
			envLines = append(envLines, fmt.Sprintf("%s=%s", k, v))
		}
		envContent := strings.Join(envLines, "\n") + "\n"
		if err := writeFileAsUser(ctx, filepath.Join(appDir, ".env"), envContent, req.User, "0600"); err != nil {
			return nil, err
		}
	}

	// Always inject PORT into the env for the started service.
	runtimeEnv := map[string]string{}
	for k, v := range req.EnvVars {
		runtimeEnv[k] = v
	}
	if req.Port > 0 {
		runtimeEnv["PORT"] = fmt.Sprintf("%d", req.Port)
	}

	// --- 8. Build ---------------------------------------------------------
	if req.BuildCmd != "" {
		if err := runBuildAsUser(ctx, req.User, appDir, req.BuildCmd); err != nil {
			return nil, err
		}
	}

	// --- 9. Serve ---------------------------------------------------------
	if isStatic {
		// For React/Vite etc., serve the build output dir directly via nginx.
		servedDir := appDir
		if hasPreset && preset.StaticDir != "" {
			servedDir = filepath.Join(appDir, preset.StaticDir)
		}
		if req.Domain != "" {
			if err := agent.CreateStaticVhost(ctx, req.Domain, servedDir); err != nil {
				return nil, fmt.Errorf("failed to create static vhost: %w", err)
			}
		}
	} else {
		// A non-static app must have a start command — otherwise we'd create
		// an nginx proxy pointing at a port that nothing is listening on,
		// giving every request a 502 Bad Gateway. Fail the deploy loudly
		// instead of silently producing a broken site.
		startCmd := renderStartCmd(req.StartCmd, req.Port)
		if strings.TrimSpace(startCmd) == "" {
			return nil, fmt.Errorf("start_cmd is required for non-static apps (pick a framework preset or supply one explicitly)")
		}
		if err := agent.CreateSystemdService(ctx, req.Name, req.User, appDir, startCmd, runtimeEnv); err != nil {
			return nil, fmt.Errorf("failed to create service: %w", err)
		}
		// Give the freshly started service a moment to bind its port before
		// pointing nginx at it, so the first HTTP request after deploy
		// doesn't race the child process into a 502.
		if req.Port > 0 {
			waitForPort(req.Port, 8*time.Second)
		}
		if req.Domain != "" && req.Port > 0 {
			if err := agent.CreateReverseProxy(ctx, &agent.VhostConfig{Domain: req.Domain, Port: req.Port}); err != nil {
				return nil, fmt.Errorf("failed to create reverse proxy: %w", err)
			}
		}
	}

	// --- 10. Persist ------------------------------------------------------
	now := time.Now()
	status := "running"
	if isStatic {
		status = "static"
	}
	app := models.App{
		Name:             req.Name,
		Domain:           req.Domain,
		Path:             req.Path,
		AppType:          req.AppType,
		Framework:        req.Framework,
		RuntimeVersion:   req.RuntimeVersion,
		DeployMethod:     req.DeployMethod,
		User:             req.User,
		Port:             req.Port,
		GitURL:           req.GitURL,
		GitBranch:        req.GitBranch,
		GitToken:         req.GitToken,
		DockerImage:      req.DockerImage,
		DockerVolumes:    req.DockerVolumes,
		DockerNetwork:    req.DockerNetwork,
		BuildCmd:         req.BuildCmd,
		StartCmd:         req.StartCmd,
		HealthCheckPath:  req.HealthCheckPath,
		MinInstances:     req.MinInstances,
		MaxInstances:     req.MaxInstances,
		EnvVars:          req.EnvVars,
		Status:           status,
		LastDeployed:     &now,
		DeploymentsCount: 1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	result, err := col.InsertOne(ctx, app)
	if err != nil {
		return nil, err
	}
	app.ID = result.InsertedID.(primitive.ObjectID)
	return &app, nil
}

func (s *AppService) Redeploy(ctx context.Context, name string) (*models.App, error) {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("app not found: %w", err)
	}

	appDir := fmt.Sprintf("/home/%s/apps/%s", app.User, app.Name)

	// Pull latest code
	if app.DeployMethod == "git" && app.GitBranch != "" {
		if err := agent.GitPull(ctx, appDir, app.GitBranch); err != nil {
			return nil, fmt.Errorf("git pull failed: %w", err)
		}
	}

	// Rebuild
	if app.BuildCmd != "" {
		if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("cd %s && %s", appDir, app.BuildCmd)); err != nil {
			return nil, fmt.Errorf("rebuild failed: %w", err)
		}
	}

	// Restart service
	serviceName := "sp-app-" + app.Name
	agent.ServiceAction(ctx, serviceName, "restart")

	now := time.Now()
	col := s.db.Collection(database.ColApps)
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var updated models.App
	err = col.FindOneAndUpdate(ctx, bson.M{"_id": app.ID}, bson.M{
		"$set": bson.M{"status": "running", "last_deployed": now, "updated_at": now},
		"$inc": bson.M{"deployments_count": 1},
	}, opts).Decode(&updated)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *AppService) Action(ctx context.Context, name string, action string) error {
	serviceName := "sp-app-" + name
	if err := agent.ServiceAction(ctx, serviceName, action); err != nil {
		return fmt.Errorf("failed to %s app: %w", action, err)
	}

	status := "running"
	if action == "stop" {
		status = "stopped"
	}

	s.db.Collection(database.ColApps).UpdateOne(ctx, bson.M{"name": name}, bson.M{
		"$set": bson.M{"status": status, "updated_at": time.Now()},
	})
	return nil
}

func (s *AppService) Delete(ctx context.Context, name string) error {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return fmt.Errorf("app not found: %w", err)
	}

	// Stop and delete systemd service. DeleteSystemdService prepends the
	// "sp-app-" prefix itself, so we pass the bare app name.
	agent.DeleteSystemdService(ctx, app.Name)

	// Delete nginx vhost
	if app.Domain != "" {
		agent.DeleteVhost(ctx, app.Domain)
	}

	// Remove app directory
	os.RemoveAll(fmt.Sprintf("/home/%s/apps/%s", app.User, app.Name))

	// Delete from database
	_, err = s.db.Collection(database.ColApps).DeleteOne(ctx, bson.M{"_id": app.ID})
	return err
}

func (s *AppService) GetLogs(ctx context.Context, name string, lines int) ([]string, error) {
	if lines <= 0 {
		lines = 100
	}
	serviceName := "sp-app-" + name
	result, err := agent.RunCommand(ctx, "journalctl", "-u", serviceName, "-n", fmt.Sprint(lines), "--no-pager")
	if err != nil {
		return []string{}, nil
	}
	logLines := strings.Split(strings.TrimSpace(result.Output), "\n")
	return logLines, nil
}

func (s *AppService) UpdateEnv(ctx context.Context, name string, envVars map[string]string, restart bool) error {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return fmt.Errorf("app not found: %w", err)
	}

	appDir := fmt.Sprintf("/home/%s/apps/%s", app.User, app.Name)

	// Write .env file
	var envLines []string
	for k, v := range envVars {
		envLines = append(envLines, fmt.Sprintf("%s=%s", k, v))
	}
	if err := os.WriteFile(fmt.Sprintf("%s/.env", appDir), []byte(strings.Join(envLines, "\n")+"\n"), 0600); err != nil {
		return fmt.Errorf("failed to write .env: %w", err)
	}

	// Update database
	s.db.Collection(database.ColApps).UpdateOne(ctx, bson.M{"_id": app.ID}, bson.M{
		"$set": bson.M{"env_vars": envVars, "updated_at": time.Now()},
	})

	// Restart if requested
	if restart {
		serviceName := "sp-app-" + name
		agent.ServiceAction(ctx, serviceName, "restart")
	}

	return nil
}

func (s *AppService) Rollback(ctx context.Context, name string, deploymentID string) error {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return fmt.Errorf("app not found: %w", err)
	}

	appDir := fmt.Sprintf("/home/%s/apps/%s", app.User, app.Name)

	// Get the target deployment
	if deploymentID != "" {
		oid, err := primitive.ObjectIDFromHex(deploymentID)
		if err == nil {
			var deployment models.AppDeployment
			if err := s.db.Collection(database.ColDeployments).FindOne(ctx, bson.M{"_id": oid}).Decode(&deployment); err == nil {
				if deployment.GitCommit != "" {
					agent.RunCommand(ctx, "git", "-C", appDir, "checkout", deployment.GitCommit)
				}
			}
		}
	}

	// Rebuild and restart
	if app.BuildCmd != "" {
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("cd %s && %s", appDir, app.BuildCmd))
	}
	serviceName := "sp-app-" + name
	agent.ServiceAction(ctx, serviceName, "restart")

	return nil
}

func (s *AppService) ListByUser(ctx context.Context, userID string, page, limit int) ([]models.App, int64, error) {
	col := s.db.Collection(database.ColApps)
	filter := bson.M{"user": userID}

	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().SetSkip(skip).SetLimit(int64(limit)).SetSort(bson.D{{Key: "created_at", Value: -1}})
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var apps []models.App
	if err := cursor.All(ctx, &apps); err != nil {
		return nil, 0, err
	}
	if apps == nil {
		apps = []models.App{}
	}
	return apps, total, nil
}
