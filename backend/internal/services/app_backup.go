package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
)

// AppBackup represents a saved backup archive on disk.
type AppBackup struct {
	File      string    `json:"file"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// backupDir returns the directory where app backups are stored.
func (s *AppService) backupDir(user, name string) string {
	return fmt.Sprintf("/home/%s/backups/apps/%s", user, name)
}

// Backup creates a timestamped tar.gz of the app's deployment directory
// (code + .env + build output) so it can be restored or transferred later.
// The systemd unit file is stored alongside as <name>.service for easy
// transfer to a different server.
func (s *AppService) Backup(ctx context.Context, name string) (*AppBackup, error) {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("app not found: %w", err)
	}

	appDir := fmt.Sprintf("/home/%s/apps/%s", app.User, app.Name)
	backupDir := s.backupDir(app.User, app.Name)
	if _, err := agent.RunCommand(ctx, "install", "-d", "-o", app.User, "-g", app.User, "-m", "0755", backupDir); err != nil {
		return nil, fmt.Errorf("prepare backup dir: %w", err)
	}

	ts := time.Now().UTC().Format("20060102-150405")
	archive := fmt.Sprintf("%s/%s-%s.tar.gz", backupDir, app.Name, ts)

	// Exclude volatile build caches that bloat backups and are rebuildable.
	excludes := []string{
		"--exclude=node_modules",
		"--exclude=.next/cache",
		"--exclude=venv",
		"--exclude=vendor/bundle",
		"--exclude=__pycache__",
	}
	args := append([]string{"-czf", archive, "-C", fmt.Sprintf("/home/%s/apps", app.User)}, excludes...)
	args = append(args, app.Name)
	if _, err := agent.RunCommand(ctx, "tar", args...); err != nil {
		return nil, fmt.Errorf("tar failed: %w", err)
	}

	// Copy the systemd unit file next to the archive so transfers carry
	// the service definition along with the code.
	unitSrc := fmt.Sprintf("/etc/systemd/system/sp-app-%s.service", app.Name)
	unitDst := fmt.Sprintf("%s/%s-%s.service", backupDir, app.Name, ts)
	agent.RunCommand(ctx, "cp", "-f", unitSrc, unitDst)
	agent.RunCommand(ctx, "chown", app.User+":"+app.User, archive, unitDst)

	res, _ := agent.RunCommand(ctx, "stat", "-c", "%s", archive)
	var size int64
	fmt.Sscanf(strings.TrimSpace(res.Output), "%d", &size)

	// Record in DB for history.
	s.db.Collection(database.ColApps).UpdateOne(ctx, bson.M{"_id": app.ID}, bson.M{
		"$set": bson.M{"last_backup_at": time.Now(), "updated_at": time.Now()},
	})

	_ = appDir
	return &AppBackup{File: archive, Size: size, CreatedAt: time.Now()}, nil
}

// ListBackups returns all stored backups for an app.
func (s *AppService) ListBackups(ctx context.Context, name string) ([]AppBackup, error) {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	backupDir := s.backupDir(app.User, app.Name)
	res, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("ls -1 %q/*.tar.gz 2>/dev/null", backupDir))
	if err != nil {
		return []AppBackup{}, nil
	}
	var out []AppBackup
	for _, line := range strings.Split(strings.TrimSpace(res.Output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		statRes, _ := agent.RunCommand(ctx, "stat", "-c", "%s %Y", line)
		var size, mtime int64
		fmt.Sscanf(strings.TrimSpace(statRes.Output), "%d %d", &size, &mtime)
		out = append(out, AppBackup{File: line, Size: size, CreatedAt: time.Unix(mtime, 0)})
	}
	return out, nil
}

// Restore reverts an app's code directory to the contents of a previous
// backup archive. The systemd service is stopped during the swap and
// restarted afterwards. The current state is saved as a safety backup first.
func (s *AppService) Restore(ctx context.Context, name, archive string) error {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return fmt.Errorf("app not found: %w", err)
	}

	backupDir := s.backupDir(app.User, app.Name)
	if !strings.HasPrefix(archive, backupDir+"/") {
		return fmt.Errorf("archive must be inside %s", backupDir)
	}
	if _, err := agent.RunCommand(ctx, "test", "-f", archive); err != nil {
		return fmt.Errorf("backup archive not found: %s", archive)
	}

	// Safety backup of current state.
	if _, err := s.Backup(ctx, name); err != nil {
		return fmt.Errorf("pre-restore snapshot failed: %w", err)
	}

	serviceName := "sp-app-" + app.Name
	agent.ServiceAction(ctx, serviceName, "stop")

	appDir := fmt.Sprintf("/home/%s/apps/%s", app.User, app.Name)
	if _, err := agent.RunCommand(ctx, "rm", "-rf", appDir); err != nil {
		return fmt.Errorf("cleanup appDir: %w", err)
	}
	if _, err := agent.RunCommand(ctx, "install", "-d", "-o", app.User, "-g", app.User, "-m", "0755", appDir); err != nil {
		return err
	}
	parent := fmt.Sprintf("/home/%s/apps", app.User)
	if _, err := agent.RunCommand(ctx, "tar", "-xzf", archive, "-C", parent); err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}
	agent.RunCommand(ctx, "chown", "-R", app.User+":"+app.User, appDir)

	// Backups intentionally exclude regenerable build artefacts
	// (node_modules, .next/cache, venv, vendor/bundle, __pycache__) to keep
	// archives small. After restoring source code we have to re-run the
	// build command, otherwise the freshly extracted tree references
	// binaries that aren't on disk and the service comes back up as 502.
	if app.BuildCmd != "" {
		if err := runBuildAsUser(ctx, app.User, appDir, app.BuildCmd); err != nil {
			return fmt.Errorf("post-restore build failed: %w", err)
		}
	}

	// For non-static apps, restart the service. Use restart (not start) so
	// it picks up the freshly rebuilt artefacts even if it was somehow
	// already running.
	if app.AppType != "static" {
		agent.ServiceAction(ctx, serviceName, "restart")
		// Block briefly until the upstream port is reachable, so the
		// reverse proxy doesn't race the just-started process into 502.
		if app.Port > 0 {
			waitForPort(app.Port, 8*time.Second)
		}
	}
	s.db.Collection(database.ColApps).UpdateOne(ctx, bson.M{"_id": app.ID}, bson.M{
		"$set": bson.M{"last_restored_at": time.Now(), "updated_at": time.Now()},
	})
	return nil
}

// TransferRequest describes where and how to move an app to a different user
// on the same server, or export it to another host.
type TransferRequest struct {
	TargetUser string `json:"target_user"` // move ownership to this user on the same server
	TargetHost string `json:"target_host"` // optional: scp the archive to this host
	TargetPath string `json:"target_path"` // remote path to drop the archive (with target_host)
	SSHKeyPath string `json:"ssh_key_path"`
}

// Transfer moves an app to a different system user on the same server. If
// TargetHost is provided, the backup archive is also scp'd to that host for
// out-of-band import. Domain/nginx/systemd unit are updated to the new user.
func (s *AppService) Transfer(ctx context.Context, name string, req *TransferRequest) (*models.App, error) {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("app not found: %w", err)
	}
	if req.TargetUser == "" && req.TargetHost == "" {
		return nil, fmt.Errorf("target_user or target_host is required")
	}

	// Always take a fresh backup first.
	bk, err := s.Backup(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("pre-transfer backup failed: %w", err)
	}

	// Optional: ship the archive to a remote host.
	if req.TargetHost != "" {
		dst := req.TargetPath
		if dst == "" {
			dst = fmt.Sprintf("/tmp/%s.tar.gz", app.Name)
		}
		scpArgs := []string{"-o", "StrictHostKeyChecking=no"}
		if req.SSHKeyPath != "" {
			scpArgs = append(scpArgs, "-i", req.SSHKeyPath)
		}
		scpArgs = append(scpArgs, bk.File, fmt.Sprintf("%s:%s", req.TargetHost, dst))
		if _, err := agent.RunCommand(ctx, "scp", scpArgs...); err != nil {
			return nil, fmt.Errorf("scp to %s failed: %w", req.TargetHost, err)
		}
	}

	// Same-server user transfer.
	if req.TargetUser != "" && req.TargetUser != app.User {
		if err := ensureUser(ctx, req.TargetUser); err != nil {
			return nil, err
		}
		serviceName := "sp-app-" + app.Name
		agent.ServiceAction(ctx, serviceName, "stop")
		// DeleteSystemdService prepends "sp-app-" itself, so we pass the bare
		// app name. Passing the already-prefixed serviceName here would try
		// to delete sp-app-sp-app-<name>, which is a no-op — the same bug
		// that previously left stale processes around after Delete.
		agent.DeleteSystemdService(ctx, app.Name)

		oldDir := fmt.Sprintf("/home/%s/apps/%s", app.User, app.Name)
		newParent := fmt.Sprintf("/home/%s/apps", req.TargetUser)
		newDir := fmt.Sprintf("%s/%s", newParent, app.Name)
		agent.RunCommand(ctx, "install", "-d", "-o", req.TargetUser, "-g", req.TargetUser, "-m", "0755", newParent)
		if _, err := agent.RunCommand(ctx, "mv", oldDir, newDir); err != nil {
			return nil, fmt.Errorf("move app dir: %w", err)
		}
		agent.RunCommand(ctx, "chown", "-R", req.TargetUser+":"+req.TargetUser, newDir)

		if app.AppType != "static" && app.StartCmd != "" {
			startCmd := renderStartCmd(app.StartCmd, app.Port)
			runtimeEnv := map[string]string{}
			for k, v := range app.EnvVars {
				runtimeEnv[k] = v
			}
			if app.Port > 0 {
				runtimeEnv["PORT"] = fmt.Sprintf("%d", app.Port)
			}
			if err := agent.CreateSystemdService(ctx, app.Name, req.TargetUser, newDir, startCmd, runtimeEnv); err != nil {
				return nil, fmt.Errorf("recreate service: %w", err)
			}
		}
		// Wait for the new service to actually bind its port before we
		// repoint nginx, to avoid the same race that caused 502s on
		// fresh deploy.
		if app.AppType != "static" && app.Port > 0 {
			waitForPort(app.Port, 8*time.Second)
		}
		// Rewrite nginx (domain + port unchanged).
		if app.Domain != "" {
			if app.AppType == "static" {
				agent.CreateStaticVhost(ctx, app.Domain, newDir)
			} else if app.Port > 0 {
				agent.CreateReverseProxy(ctx, &agent.VhostConfig{Domain: app.Domain, Port: app.Port})
			}
		}

		s.db.Collection(database.ColApps).UpdateOne(ctx, bson.M{"_id": app.ID}, bson.M{
			"$set": bson.M{"user": req.TargetUser, "updated_at": time.Now()},
		})
		app.User = req.TargetUser
	}

	return app, nil
}
