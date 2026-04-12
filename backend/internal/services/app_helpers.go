package services

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
)

var appNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// validateAppName ensures the app name is safe for use in systemd unit names,
// shell arguments, and filesystem paths. Lowercase alphanumeric with dashes,
// must start with a letter, 2-32 chars.
func validateAppName(name string) error {
	if !appNamePattern.MatchString(name) {
		return fmt.Errorf("invalid app name %q: must be 2-32 chars, lowercase, start with a letter, only a-z 0-9 and '-'", name)
	}
	return nil
}

// normalizeAppType canonicalises type synonyms sent from different frontends.
func normalizeAppType(t string) string {
	switch strings.ToLower(t) {
	case "nodejs", "node":
		return "node"
	case "nextjs":
		return "node"
	default:
		return strings.ToLower(t)
	}
}

// sanitizeDomain trims and lowercases a domain for nginx consistency.
func sanitizeDomain(d string) string {
	return strings.ToLower(strings.TrimSpace(d))
}

// ensureUser creates a system user if it does not already exist. The home
// directory is created at /home/<user> and a dedicated group is created with
// the same name. No-op if the user exists.
func ensureUser(ctx context.Context, user string) error {
	if user == "" {
		return fmt.Errorf("system user is required")
	}
	// Check if user exists via `id`
	if _, err := agent.RunCommand(ctx, "id", user); err == nil {
		return nil
	}
	if _, err := agent.RunCommand(ctx, "useradd", "-m", "-s", "/bin/bash", user); err != nil {
		return fmt.Errorf("failed to create user %s: %w", user, err)
	}
	return nil
}

// allocatePort picks a free TCP port in the 3100-3999 range that is not
// currently bound and not already used by another deployed app in MongoDB.
// Falls back to any free port if the preferred range is exhausted.
func allocatePort(usedPorts map[int]bool) (int, error) {
	for port := 3100; port < 4000; port++ {
		if usedPorts[port] {
			continue
		}
		if isPortFree(port) {
			return port, nil
		}
	}
	// Fallback: kernel-assigned
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func isPortFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// writeFileAsUser writes a file with contents owned by the given user. It
// uses shell redirection so the ServerPanel process (running as root) can
// create files that the app user can subsequently read or execute.
func writeFileAsUser(ctx context.Context, path, contents, user string, mode string) error {
	// Ensure parent dir exists with correct ownership.
	dir := filepath.Dir(path)
	if _, err := agent.RunCommand(ctx, "install", "-d", "-o", user, "-g", user, "-m", "0755", dir); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	// Write via heredoc to avoid quoting issues with arbitrary content.
	marker := "SP_SCAFFOLD_EOF"
	script := fmt.Sprintf("cat > %q <<'%s'\n%s\n%s\n", path, marker, contents, marker)
	if _, err := agent.RunCommand(ctx, "bash", "-c", script); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := agent.RunCommand(ctx, "chown", user+":"+user, path); err != nil {
		return err
	}
	if mode != "" {
		if _, err := agent.RunCommand(ctx, "chmod", mode, path); err != nil {
			return err
		}
	}
	return nil
}

// prepareAppDir removes any prior deployment at appDir and recreates it with
// the correct ownership, so a fresh git clone or scaffold write can succeed.
func prepareAppDir(ctx context.Context, appDir, user string) error {
	if _, err := agent.RunCommand(ctx, "rm", "-rf", appDir); err != nil {
		return fmt.Errorf("cleanup %s: %w", appDir, err)
	}
	if _, err := agent.RunCommand(ctx, "install", "-d", "-o", user, "-g", user, "-m", "0755", appDir); err != nil {
		return fmt.Errorf("mkdir %s: %w", appDir, err)
	}
	// Also ensure /home/<user>/apps exists with correct ownership.
	parent := filepath.Dir(appDir)
	agent.RunCommand(ctx, "install", "-d", "-o", user, "-g", user, "-m", "0755", parent)
	return nil
}

// chownRecursive sets ownership of appDir and everything inside it to user:user.
func chownRecursive(ctx context.Context, appDir, user string) error {
	_, err := agent.RunCommand(ctx, "chown", "-R", user+":"+user, appDir)
	return err
}

// runBuildAsUser runs the build command inside appDir as the app user. Uses
// `sudo -u` + login-like shell so NVM/rbenv/pyenv etc. still work, and
// prepends /usr/local/bin to PATH so node/npm (installed there) are found.
func runBuildAsUser(ctx context.Context, user, appDir, buildCmd string) error {
	script := fmt.Sprintf("export PATH=/usr/local/bin:/usr/bin:/bin:$PATH; export HOME=/home/%s; cd %q && %s", user, appDir, buildCmd)
	res, err := agent.RunCommand(ctx, "sudo", "-u", user, "-H", "bash", "-lc", script)
	if err != nil {
		tail := res.Error
		if len(tail) > 800 {
			tail = tail[len(tail)-800:]
		}
		return fmt.Errorf("build failed: %s", tail)
	}
	return nil
}

// buildStaticVhostConfig returns the nginx server block contents for a
// static site served directly from disk with a SPA fallback.
func buildStaticVhostConfig(domain, root string) string {
	return fmt.Sprintf(`server {
    listen 80;
    server_name %s;
    root %s;
    index index.html;

    access_log /var/log/nginx/%s-access.log;
    error_log /var/log/nginx/%s-error.log;

    location / {
        try_files $uri $uri/ /index.html;
    }
}
`, domain, root, domain, domain)
}
