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

// appInstallDir returns the on-disk directory for an app's code. Uses the
// per-app InstallPath override if set, otherwise falls back to the legacy
// convention /home/{user}/apps/{name}. Older app records (pre-InstallPath)
// have an empty field and keep working via the fallback.
func appInstallDir(app *models.App) string {
	if app.InstallPath != "" {
		return app.InstallPath
	}
	return fmt.Sprintf("/home/%s/apps/%s", app.User, app.Name)
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
	// If the caller specified a custom install path, honor it after some
	// basic sanity checks; otherwise use the legacy /home/{user}/apps/{name}
	// layout so existing deploys keep working unchanged.
	appDir := strings.TrimSpace(req.InstallPath)
	if appDir != "" {
		if !filepath.IsAbs(appDir) {
			return nil, fmt.Errorf("install_path must be an absolute path (e.g. /home/%s/apps/%s)", req.User, req.Name)
		}
		appDir = filepath.Clean(appDir)
		// Refuse obviously dangerous targets that would clobber the system
		// or the panel itself. This is not a sandbox — the admin can still
		// point anywhere writable — just a guard against typos like "/".
		switch appDir {
		case "/", "/root", "/etc", "/usr", "/var", "/bin", "/sbin", "/lib", "/boot", "/home":
			return nil, fmt.Errorf("install_path %q is not allowed", appDir)
		}
	} else {
		appDir = fmt.Sprintf("/home/%s/apps/%s", req.User, req.Name)
	}
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
	var servedDir string
	if isStatic {
		// For React/Vite etc., serve the build output dir directly via nginx.
		servedDir = appDir
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
		// Node apps are launched under PM2 so the process gets crash-restart,
		// throttling, and memory limits on top of the systemd Restart=always
		// loop. Any other runtime (python, ruby, go binaries, ...) keeps the
		// plain ExecStart path it had before.
		if req.AppType == "node" {
			ecosystem := buildPM2Ecosystem(req.Name, startCmd, appDir, req.Port, req.EnvVars)
			if err := writeFileAsUser(ctx, filepath.Join(appDir, "ecosystem.config.js"), ecosystem, req.User, "0644"); err != nil {
				return nil, fmt.Errorf("write ecosystem.config.js: %w", err)
			}
			startCmd = "pm2-runtime start ecosystem.config.js"
			// Per-app PM2_HOME so each app's pm2-runtime gets its own
			// daemon directory. Without this, two apps on the same Linux
			// user share /home/<user>/.pm2 and the second pm2-runtime
			// adopts the first's process list — restarting one then
			// kills the other. Verified live on the VPS with two node
			// apps (hgbyiiiii + kjbihbhyb) before this fix.
			runtimeEnv["PM2_HOME"] = filepath.Join(appDir, ".pm2")
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

	// --- 9b. Auto-issue Let's Encrypt SSL ---------------------------------
	// HTTP-01 needs the domain to actually resolve to this server, so this is
	// best-effort: log and continue if it fails. The user can re-issue later
	// from the SSL page. When the cert lands we recreate the vhost with the
	// SSL variant so port 443 starts answering immediately.
	if req.Domain != "" {
		ensureSSLForApp(ctx, s.db, req.Domain, isStatic, req.Port, servedDir)
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
		InstallPath:      appDir,
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

	appDir := appInstallDir(app)

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

	// Regenerate ecosystem.config.js for node apps on every redeploy. The
	// File Manager's Extract happily overwrites or wipes this file when an
	// operator uploads a project archive into the app dir — after which
	// pm2-runtime crash-loops with ENOENT. Writing it back here makes
	// redeploy the canonical "fix broken node app" action instead of
	// needing the operator to hand-craft the file on the VPS.
	if app.AppType == "node" {
		startCmd := renderStartCmd(app.StartCmd, app.Port)
		// Some user projects ship a custom-server Next.js (server.js) even
		// though the nextjs preset's default start_cmd is `npx next start`.
		// When package.json's "start" script points at server.js, prefer
		// it — `next start` won't work against a custom server build.
		if customCmd := detectCustomNodeStart(ctx, appDir); customCmd != "" {
			startCmd = customCmd
		}
		if strings.TrimSpace(startCmd) != "" {
			ecosystem := buildPM2Ecosystem(app.Name, startCmd, appDir, app.Port, app.EnvVars)
			if err := writeFileAsUser(ctx, filepath.Join(appDir, "ecosystem.config.js"), ecosystem, app.User, "0644"); err != nil {
				// Non-fatal — keep trying to restart; the existing file (if
				// any) may still be serviceable.
				fmt.Fprintf(os.Stderr, "warning: failed to regenerate ecosystem.config.js for %s: %v\n", app.Name, err)
			}
		}
	}

	// Re-chown the app dir to the app user. File Manager uploads run as
	// root and leave files owned by root, which makes `next build`
	// (running as the app user) fail with EACCES when it tries to
	// rewrite .next/. Quietly fixing it here means a Redeploy heals an
	// app that was broken by an upload+overwrite.
	chownRecursive(ctx, appDir, app.User)

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

// InstallPackagesResult is returned by InstallPackages so the handler can
// forward both the captured output and the success flag to the UI.
type InstallPackagesResult struct {
	Command    string `json:"command"`
	Output     string `json:"output"`
	Success    bool   `json:"success"`
	DurationMs int64  `json:"duration_ms"`
}

// InstallPackages runs a package install step for a deployed app without
// touching the systemd unit, nginx vhost, or DB status. If customCmd is
// empty, it falls back to the app's original build_cmd. When a node app is
// started under pm2-runtime, the caller is expected to restart it manually
// (the UI exposes a separate Restart action).
func (s *AppService) InstallPackages(ctx context.Context, name, customCmd string) (*InstallPackagesResult, error) {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("app not found: %w", err)
	}
	cmd := strings.TrimSpace(customCmd)
	if cmd == "" {
		cmd = strings.TrimSpace(app.BuildCmd)
	}
	if cmd == "" {
		return nil, fmt.Errorf("no install command provided and app has no build_cmd")
	}

	appDir := appInstallDir(app)
	start := time.Now()
	output, ok := runInstallAsUser(ctx, app.User, appDir, cmd)
	return &InstallPackagesResult{
		Command:    cmd,
		Output:     output,
		Success:    ok,
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
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

	// Replace the app's nginx vhost with a "site not deployed" placeholder
	// instead of removing it outright. Removing the vhost makes nginx fall
	// back to whatever 443 server block sorts first alphabetically, which
	// then serves the WRONG site's cert for this domain (browser shows
	// NET::ERR_CERT_COMMON_NAME_INVALID). The placeholder uses the
	// domain's own (preserved) Let's Encrypt cert, so HTTPS keeps working
	// and the visitor sees a clear "Site not deployed" page until the
	// operator re-deploys or removes the vhost manually.
	if app.Domain != "" {
		agent.WritePlaceholderVhost(ctx, app.Domain)
	}

	// PRESERVE the app directory at /home/<user>/apps/<name>. Auto-deleting
	// user-uploaded code on every Delete is a data-loss / security risk:
	// an accidental click in the UI was destroying source the operator
	// uploaded. The DB record is removed below so the namespace is free
	// for a fresh deploy; the files are left for the operator to inspect
	// and remove via the File Manager when they're sure.

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

	appDir := appInstallDir(app)

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

// UpdateAppRequest is the JSON body for PUT /apps/:name. Every field is
// optional — only non-nil fields are applied, so the frontend can send
// partial patches (e.g. just BuildCmd) without clobbering everything else.
type UpdateAppRequest struct {
	Domain          *string            `json:"domain"`
	Path            *string            `json:"path"`
	BuildCmd        *string            `json:"build_cmd"`
	StartCmd        *string            `json:"start_cmd"`
	HealthCheckPath *string            `json:"health_check_path"`
	GitURL          *string            `json:"git_url"`
	GitBranch       *string            `json:"git_branch"`
	EnvVars         *map[string]string `json:"env_vars"`
	Restart         bool               `json:"restart"`
}

// Update edits an existing app's mutable fields and (if requested) writes
// .env + regenerates ecosystem.config.js + restarts. Used by the WHM Edit
// modal so operators can fix typos in build_cmd / start_cmd / domain
// without having to delete and re-deploy.
func (s *AppService) Update(ctx context.Context, name string, req *UpdateAppRequest) (*models.App, error) {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("app not found: %w", err)
	}
	appDir := appInstallDir(app)

	set := bson.M{"updated_at": time.Now()}
	if req.Domain != nil {
		app.Domain = sanitizeDomain(*req.Domain)
		set["domain"] = app.Domain
	}
	if req.Path != nil {
		app.Path = *req.Path
		set["path"] = app.Path
	}
	if req.BuildCmd != nil {
		app.BuildCmd = *req.BuildCmd
		set["build_cmd"] = app.BuildCmd
	}
	if req.StartCmd != nil {
		app.StartCmd = *req.StartCmd
		set["start_cmd"] = app.StartCmd
	}
	if req.HealthCheckPath != nil {
		app.HealthCheckPath = *req.HealthCheckPath
		set["health_check_path"] = app.HealthCheckPath
	}
	if req.GitURL != nil {
		app.GitURL = *req.GitURL
		set["git_url"] = app.GitURL
	}
	if req.GitBranch != nil {
		app.GitBranch = *req.GitBranch
		set["git_branch"] = app.GitBranch
	}
	if req.EnvVars != nil {
		app.EnvVars = *req.EnvVars
		set["env_vars"] = app.EnvVars
		// Persist .env on disk so the running process sees them after restart.
		var envLines []string
		for k, v := range app.EnvVars {
			envLines = append(envLines, fmt.Sprintf("%s=%s", k, v))
		}
		writeFileAsUser(ctx, filepath.Join(appDir, ".env"), strings.Join(envLines, "\n")+"\n", app.User, "0600")
	}

	// Regenerate ecosystem.config.js for node apps so a changed start_cmd /
	// env actually takes effect on next start. Other runtimes use the
	// systemd unit's ExecStart directly which only changes on Redeploy.
	if app.AppType == "node" && (req.StartCmd != nil || req.EnvVars != nil) {
		startCmd := renderStartCmd(app.StartCmd, app.Port)
		if customCmd := detectCustomNodeStart(ctx, appDir); customCmd != "" {
			startCmd = customCmd
		}
		if strings.TrimSpace(startCmd) != "" {
			ecosystem := buildPM2Ecosystem(app.Name, startCmd, appDir, app.Port, app.EnvVars)
			writeFileAsUser(ctx, filepath.Join(appDir, "ecosystem.config.js"), ecosystem, app.User, "0644")
		}
	}

	if _, err := s.db.Collection(database.ColApps).UpdateOne(ctx, bson.M{"_id": app.ID}, bson.M{"$set": set}); err != nil {
		return nil, fmt.Errorf("db update failed: %w", err)
	}

	if req.Restart {
		serviceName := "sp-app-" + app.Name
		agent.ServiceAction(ctx, serviceName, "restart")
	}

	return s.GetByName(ctx, name)
}

func (s *AppService) Rollback(ctx context.Context, name string, deploymentID string) error {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return fmt.Errorf("app not found: %w", err)
	}

	appDir := appInstallDir(app)

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
