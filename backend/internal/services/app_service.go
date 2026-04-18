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

// appWorkDir returns the directory commands actually run from. For
// monorepo deploys (RepoSubpath set) this is <installDir>/<subpath>;
// otherwise it's just the install dir. Used as the systemd
// WorkingDirectory and the cwd of every install/build/start command so
// `npm install` reads the subpath's package.json, not the monorepo root.
func appWorkDir(app *models.App) string {
	base := appInstallDir(app)
	sub, _ := validateRepoSubpath(app.RepoSubpath)
	if sub == "" {
		return base
	}
	return filepath.Join(base, sub)
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

// GetByWebhookID looks up an app by its webhook URL identifier. Used by
// the public POST /api/v1/webhooks/github/:id endpoint to find the
// target before HMAC-verifying the payload. Returns mongo.ErrNoDocuments
// when no app matches — handler should treat that as 404 (not 401, to
// avoid leaking which IDs exist).
func (s *AppService) GetByWebhookID(ctx context.Context, webhookID string) (*models.App, error) {
	col := s.db.Collection(database.ColApps)
	var app models.App
	if err := col.FindOne(ctx, bson.M{"webhook_id": webhookID}).Decode(&app); err != nil {
		return nil, err
	}
	return &app, nil
}

// EnableWebhook turns on auto-deploy for an existing app, generating a
// fresh ID + secret. Returns the new (id, secret) pair so the handler
// can show them to the operator once. Calling it on an app that already
// has webhook credentials regenerates them — both the URL and secret
// rotate, which is the right behavior for "rotate this leaked secret".
func (s *AppService) EnableWebhook(ctx context.Context, name string) (string, string, error) {
	id := secureRandomHex(16)
	secret := secureRandomHex(32)
	res, err := s.db.Collection(database.ColApps).UpdateOne(ctx,
		bson.M{"name": name},
		bson.M{"$set": bson.M{
			"auto_deploy":    true,
			"webhook_id":     id,
			"webhook_secret": secret,
			"updated_at":     time.Now(),
		}},
	)
	if err != nil {
		return "", "", err
	}
	if res.MatchedCount == 0 {
		return "", "", fmt.Errorf("app %q not found", name)
	}
	return id, secret, nil
}

// DisableWebhook turns off auto-deploy and clears the credentials so a
// stolen secret can no longer trigger deploys.
func (s *AppService) DisableWebhook(ctx context.Context, name string) error {
	_, err := s.db.Collection(database.ColApps).UpdateOne(ctx,
		bson.M{"name": name},
		bson.M{"$set": bson.M{
			"auto_deploy":    false,
			"webhook_id":     "",
			"webhook_secret": "",
			"updated_at":     time.Now(),
		}},
	)
	return err
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

	// Normalise + validate the monorepo subpath up front so a bad value
	// fails before we touch the filesystem. validateRepoSubpath returns ""
	// for the no-subpath case (regular single-app repos) — that keeps the
	// rest of the flow identical to the pre-monorepo behavior.
	cleanSubpath, err := validateRepoSubpath(req.RepoSubpath)
	if err != nil {
		return nil, err
	}
	req.RepoSubpath = cleanSubpath

	// Interpreted / build-step runtimes need an install/build step before
	// they can start (npm install, pip install, bundle install, go build,
	// ...). Reject deploys that leave BOTH install_cmd and build_cmd empty
	// for these types so operators don't end up with a broken service that
	// ExecStart can't find. Presets set defaults below, so this only fires
	// for deploy_method=local / git without a framework selected.
	if strings.TrimSpace(req.InstallCmd) == "" && strings.TrimSpace(req.BuildCmd) == "" && req.Framework == "" {
		if hint, ok := missingBuildCmdHint(req.AppType); ok {
			return nil, fmt.Errorf("%s apps require an install or build command (e.g. %q) — set one below, or pick a Framework preset to auto-fill it", req.AppType, hint)
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
			if req.InstallCmd == "" {
				req.InstallCmd = p.InstallCmd
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

	// workDir is where every install/build/start command runs from. For
	// regular single-app repos this is the install dir (legacy behavior);
	// for monorepos with RepoSubpath set it becomes <installDir>/<subpath>
	// so package.json detection and `npm install` see the actual app's
	// manifest, not the monorepo root's.
	workDir := appDir
	if req.RepoSubpath != "" {
		workDir = filepath.Join(appDir, req.RepoSubpath)
		// Verify the subpath exists after fetch — git repos with a missing
		// subpath would otherwise silently install from the wrong dir.
		if _, err := agent.RunCommand(ctx, "test", "-d", workDir); err != nil {
			return nil, fmt.Errorf("repo_subpath %q does not exist inside the deployed source", req.RepoSubpath)
		}
	}

	// --- 6.5. Auto-detect from package.json ------------------------------
	// For Node / Next.js / Vite / CRA repos we read framework, port,
	// scripts, and lockfile-aware install commands out of the just-cloned
	// package.json and use them as defaults for any field the operator
	// left blank. Anything they did provide wins. When detection picks a
	// framework, we also re-apply the matching preset so empty start_cmd /
	// build_cmd still get sensible defaults (the package.json alone may
	// only contain scripts.build, or only scripts.start).
	hints := DetectPackageJSONHints(workDir)
	if summary := applyPkgHints(&hints, &req.Framework, &req.InstallCmd, &req.BuildCmd, &req.StartCmd, &req.Port); summary != "" {
		fmt.Fprintf(os.Stderr, "[app %s] %s\n", req.Name, summary)
	}
	if req.Framework != "" && !hasPreset {
		if p, ok := lookupPreset(req.Framework); ok {
			preset = p
			hasPreset = true
			if req.AppType == "" {
				req.AppType = p.AppType
			}
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
			// If the detected framework is static (Vite/CRA) but the
			// caller passed app_type=node, flip it so the downstream
			// "static" branch below runs and we don't create a pointless
			// systemd unit for a pure SPA build.
			if p.IsStatic {
				isStatic = true
				req.AppType = "static"
			}
		}
	}
	// If package.json specified a concrete port, it overrides whatever port
	// was allocated back in step 3 — the app is going to bind there
	// regardless, and nginx must proxy to that port to avoid 502s.
	if hints.Port > 0 && hints.Port != 0 {
		req.Port = hints.Port
	}

	// --- 7. Env file ------------------------------------------------------
	if len(req.EnvVars) > 0 {
		var envLines []string
		for k, v := range req.EnvVars {
			envLines = append(envLines, fmt.Sprintf("%s=%s", k, v))
		}
		envContent := strings.Join(envLines, "\n") + "\n"
		// .env lands in the workDir so process.env reads it from the same
		// directory the start command runs in. For monorepos that means
		// <installDir>/<subpath>/.env, NOT the repo root.
		if err := writeFileAsUser(ctx, filepath.Join(workDir, ".env"), envContent, req.User, "0600"); err != nil {
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

	// --- 8. Install + Build ----------------------------------------------
	// Install step runs first (go mod download, npm install, pip install,
	// bundle install, …). Its failure is reported with the "install failed"
	// prefix so the UI can tell which of the two steps broke.
	//
	// runtimeBinDir pins this app's interpreter to the exact version the
	// operator picked in the Advanced Options panel. Empty string means
	// "use whatever /usr/local/bin points at", which is the pre-override
	// behavior and what un-pinned apps continue to get.
	runtimeBinDir := resolveRuntimeBinDir(req.AppType, req.RuntimeVersion)
	// Make the pinned interpreter visible to the running service too —
	// without this, the systemd unit's bash wrapper would fall back to
	// /usr/local/bin/node (latest) even though the build ran under the
	// operator-chosen version.
	if runtimeBinDir != "" {
		runtimeEnv["PATH"] = runtimeBinDir + ":/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
	}
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

	// --- 9. Serve ---------------------------------------------------------
	var servedDir string
	if isStatic {
		// For React/Vite etc., serve the build output dir directly via nginx.
		// Anchored on workDir so monorepo apps serve <installDir>/<subpath>/dist
		// instead of the repo root.
		servedDir = workDir
		if hasPreset && preset.StaticDir != "" {
			servedDir = filepath.Join(workDir, preset.StaticDir)
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
		// Pre-flight: for direct `node X.js` commands, stat the entry file
		// before creating the systemd unit. Otherwise a typo or wrong
		// subpath lands as a restart-looping service behind a reverse-proxy
		// serving 502s until the operator SSHes in. Same path the Deploy
		// Software flow uses — keep both surfaces consistent.
		if entry := extractNodeEntry(startCmd); entry != "" {
			if _, statErr := os.Stat(filepath.Join(appDir, entry)); os.IsNotExist(statErr) {
				// Fall back to the cloned package.json's scripts.start if
				// its referenced entry actually exists — that's almost
				// always the right one when the operator guessed wrong.
				if hints.StartCmd != "" && hints.StartCmd != req.StartCmd {
					if hintEntry := extractNodeEntry(hints.StartCmd); hintEntry != "" {
						if _, e2 := os.Stat(filepath.Join(appDir, hintEntry)); e2 == nil {
							req.StartCmd = hints.StartCmd
							startCmd = renderStartCmd(req.StartCmd, req.Port)
							fmt.Fprintf(os.Stderr, "[app %s] start_cmd switched to package.json scripts.start (%s was missing)\n", req.Name, entry)
						}
					}
				}
				if entry := extractNodeEntry(startCmd); entry != "" {
					if _, e := os.Stat(filepath.Join(appDir, entry)); os.IsNotExist(e) {
						return nil, &BuildError{
							Stage:   "start",
							Summary: fmt.Sprintf("start command references %q but that file isn't in the app directory", entry),
							Details: fmt.Sprintf("Your start command is:\n  %s\n\nBut %s doesn't exist at %s.\n\nFix one of:\n  1. Change start_cmd to point at your real entry file (e.g. node index.js / node dist/server.js)\n  2. Add a scripts.start to package.json that runs the correct entry\n  3. If using a monorepo, set install_path to the directory that contains the entry file", startCmd, entry, appDir),
						}
					}
				}
			}
		}
		// Node apps are launched under PM2 so the process gets crash-restart,
		// throttling, and memory limits on top of the systemd Restart=always
		// loop. Any other runtime (python, ruby, go binaries, ...) keeps the
		// plain ExecStart path it had before.
		if req.AppType == "node" {
			ecosystem := buildPM2Ecosystem(req.Name, startCmd, workDir, req.Port, req.EnvVars, req.MinInstances, req.MaxInstances)
			if err := writeFileAsUser(ctx, filepath.Join(workDir, "ecosystem.config.js"), ecosystem, req.User, "0644"); err != nil {
				return nil, fmt.Errorf("write ecosystem.config.js: %w", err)
			}
			startCmd = "pm2-runtime start ecosystem.config.js"
			// Per-app PM2_HOME so each app's pm2-runtime gets its own
			// daemon directory. Without this, two apps on the same Linux
			// user share /home/<user>/.pm2 and the second pm2-runtime
			// adopts the first's process list — restarting one then
			// kills the other. Verified live on the VPS with two node
			// apps (hgbyiiiii + kjbihbhyb) before this fix.
			runtimeEnv["PM2_HOME"] = filepath.Join(workDir, ".pm2")
		}
		if err := agent.CreateSystemdService(ctx, req.Name, req.User, workDir, startCmd, runtimeEnv); err != nil {
			return nil, fmt.Errorf("failed to create service: %w", err)
		}
		// Give the freshly started service a moment to bind its port before
		// pointing nginx at it, so the first HTTP request after deploy
		// doesn't race the child process into a 502. If nothing binds
		// within the window, capture the systemd journal so the operator
		// sees the actual crash reason instead of a mystery 502.
		if req.Port > 0 {
			if !waitForPort(req.Port, 8*time.Second) {
				unitName := "sp-app-" + req.Name
				journal := fetchUnitJournal(ctx, unitName, 40)
				if journal != "" {
					summary := summariseBuildOutput(journal)
					if summary == "" {
						summary = "backend didn't bind port " + fmt.Sprintf("%d", req.Port) + " within 8s"
					}
					agent.RunCommand(context.Background(), "systemctl", "stop", unitName)
					agent.DeleteSystemdService(context.Background(), req.Name)
					return nil, &BuildError{
						Stage:   "start",
						Summary: summary,
						Details: journal,
					}
				}
			}
		}
		// Post-deploy health probe. If the operator set a health_check_path
		// we HTTP-GET it and log (but don't fail) a warning on non-2xx/3xx.
		// This catches "port is open but the app crashed at startup" cases
		// where the listener is up (from Node's http module) before the
		// user's routes finish wiring, so the reverse proxy would serve
		// 500s to the first visitor. Warning-only by design — the operator
		// may run an app that 401s on /, which is still "working".
		if req.Port > 0 && req.HealthCheckPath != "" {
			if code, ok := probeHealthEndpoint(req.Port, req.HealthCheckPath, 4*time.Second); !ok {
				fmt.Fprintf(os.Stderr, "warning: health probe for %s at %s returned status=%d — deploy continuing but the app may not be ready\n", req.Name, req.HealthCheckPath, code)
			}
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

	// Mint webhook credentials when AutoDeploy is requested. WebhookID
	// goes in the public URL (so it's unguessable), WebhookSecret signs
	// the GitHub payload via HMAC-SHA256. Both are 16/32 random bytes —
	// matches GitHub's recommended secret length.
	var webhookID, webhookSecret string
	if req.AutoDeploy {
		webhookID = secureRandomHex(16)
		webhookSecret = secureRandomHex(32)
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
		RepoSubpath:      req.RepoSubpath,
		AutoDeploy:       req.AutoDeploy,
		WebhookID:        webhookID,
		WebhookSecret:    webhookSecret,
		DockerImage:      req.DockerImage,
		DockerVolumes:    req.DockerVolumes,
		DockerNetwork:    req.DockerNetwork,
		InstallCmd:       req.InstallCmd,
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

// LookupWebhookSecret returns the freshly-generated webhook secret for the
// just-deployed app so the handler can include it in the deploy response
// (the App's `json:"-"` tag redacts it on the wire). webhookSecret is the
// raw value from the in-memory App object, never reloaded from the DB.
func (s *AppService) LookupWebhookSecret(app *models.App) string {
	return app.WebhookSecret
}

func (s *AppService) Redeploy(ctx context.Context, name string) (*models.App, error) {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("app not found: %w", err)
	}

	appDir := appInstallDir(app)
	workDir := appWorkDir(app)

	// Pull latest code. git pull always runs against the install dir
	// (where .git lives) — the subpath only affects where build/start
	// run. Monorepo redeploys still get the whole repo refreshed.
	if app.DeployMethod == "git" && app.GitBranch != "" {
		if err := agent.GitPull(ctx, appDir, app.GitBranch); err != nil {
			return nil, fmt.Errorf("git pull failed: %w", err)
		}
	}

	// Re-install dependencies, then rebuild. Running install first on every
	// redeploy catches cases where the operator added a new package to
	// package.json / go.mod / requirements.txt and pushed the updated
	// manifest — a plain `npm run build` wouldn't fetch the new dep.
	runtimeBinDir := resolveRuntimeBinDir(app.AppType, app.RuntimeVersion)
	if app.InstallCmd != "" {
		if err := runBuildAsUser(ctx, app.User, workDir, app.InstallCmd, runtimeBinDir); err != nil {
			return nil, fmt.Errorf("reinstall step %w", err)
		}
	}
	if app.BuildCmd != "" {
		if err := runBuildAsUser(ctx, app.User, workDir, app.BuildCmd, runtimeBinDir); err != nil {
			return nil, fmt.Errorf("rebuild %w", err)
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
		if customCmd := detectCustomNodeStart(ctx, workDir); customCmd != "" {
			startCmd = customCmd
		}
		if strings.TrimSpace(startCmd) != "" {
			ecosystem := buildPM2Ecosystem(app.Name, startCmd, workDir, app.Port, app.EnvVars, app.MinInstances, app.MaxInstances)
			if err := writeFileAsUser(ctx, filepath.Join(workDir, "ecosystem.config.js"), ecosystem, app.User, "0644"); err != nil {
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
		// Prefer the dedicated install_cmd when the app was deployed via the
		// new three-command preset shape. Fall back to build_cmd for older
		// apps whose install step was baked into build_cmd.
		cmd = strings.TrimSpace(app.InstallCmd)
	}
	if cmd == "" {
		cmd = strings.TrimSpace(app.BuildCmd)
	}
	if cmd == "" {
		return nil, fmt.Errorf("no install command provided and app has no install_cmd or build_cmd")
	}

	appDir := appInstallDir(app)
	start := time.Now()
	output, ok := runInstallAsUser(ctx, app.User, appDir, cmd, resolveRuntimeBinDir(app.AppType, app.RuntimeVersion))
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
	InstallCmd      *string            `json:"install_cmd"`
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
	if req.InstallCmd != nil {
		app.InstallCmd = *req.InstallCmd
		set["install_cmd"] = app.InstallCmd
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
			ecosystem := buildPM2Ecosystem(app.Name, startCmd, appDir, app.Port, app.EnvVars, app.MinInstances, app.MaxInstances)
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

	// Reinstall + rebuild, then restart. Run both as the app user so
	// node_modules / go.sum / venv end up with the right ownership.
	rbd := resolveRuntimeBinDir(app.AppType, app.RuntimeVersion)
	if app.InstallCmd != "" {
		_ = runBuildAsUser(ctx, app.User, appDir, app.InstallCmd, rbd)
	}
	if app.BuildCmd != "" {
		_ = runBuildAsUser(ctx, app.User, appDir, app.BuildCmd, rbd)
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
