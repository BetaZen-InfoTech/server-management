package agent

import (
	"context"
	"fmt"
	"os"
)

func CreateLinuxUser(ctx context.Context, username, password string) error {
	if _, err := RunCommand(ctx, "useradd", "-m", "-s", "/bin/bash", username); err != nil {
		return err
	}
	_, err := RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo '%s:%s' | chpasswd", username, password))
	return err
}

// SetLinuxUserPassword updates the Linux account password for an existing
// user via chpasswd. Used by the WHM admin "Reset password" action so SSH
// and FTP credentials stay in sync with the panel password.
func SetLinuxUserPassword(ctx context.Context, username, password string) error {
	_, err := RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("echo '%s:%s' | chpasswd", username, password))
	return err
}

func DeleteLinuxUser(ctx context.Context, username string) error {
	_, err := RunCommand(ctx, "userdel", "-r", username)
	return err
}

func CreateUserDirectories(ctx context.Context, username string) error {
	dirs := []string{"domains", "logs", "tmp", "apps", "backups", "ssl", "mail"}
	for _, d := range dirs {
		path := fmt.Sprintf("/home/%s/%s", username, d)
		os.MkdirAll(path, 0755)
	}
	_, _ = RunCommand(ctx, "chown", "-R", username+":"+username, fmt.Sprintf("/home/%s", username))
	// Set 711 so nginx (www-data) can traverse into domain directories
	_, err := RunCommand(ctx, "chmod", "711", fmt.Sprintf("/home/%s", username))
	return err
}

func CreateDomainDirectory(ctx context.Context, username, domain string) error {
	domainRoot := fmt.Sprintf("/home/%s/domains/%s/public_html", username, domain)
	os.MkdirAll(domainRoot, 0755)

	defaultHTML := `<!DOCTYPE html><html><head><title>Welcome</title></head><body><h1>Welcome to your new website!</h1></body></html>`
	os.WriteFile(domainRoot+"/index.html", []byte(defaultHTML), 0644)

	return EnsureWebPerms(ctx, username, domain)
}

// EnsureWebPerms normalises ownership and permissions on a domain directory so
// that nginx (www-data) can serve files and the domain user can write them.
//
//	/home/<user>            → 711  (traversable, not listable)
//	/home/<user>/domains/<d> recursively chowned to <user>:<user>
//	directories             → 755  (other rx)
//	files                   → 644  (other r)
//
// Used after every flow that creates or replaces site files: fresh install,
// WordPress install, restore from backup, and transfer between servers.
func EnsureWebPerms(ctx context.Context, username, domain string) error {
	domainPath := fmt.Sprintf("/home/%s/domains/%s", username, domain)

	if _, err := RunCommand(ctx, "chmod", "711", fmt.Sprintf("/home/%s", username)); err != nil {
		return err
	}
	if _, err := RunCommand(ctx, "chown", "-R", username+":"+username, domainPath); err != nil {
		return err
	}
	if _, err := RunCommand(ctx, "bash", "-c", fmt.Sprintf("find '%s' -type d -exec chmod 755 {} +", domainPath)); err != nil {
		return err
	}
	if _, err := RunCommand(ctx, "bash", "-c", fmt.Sprintf("find '%s' -type f -exec chmod 644 {} +", domainPath)); err != nil {
		return err
	}
	return nil
}

// EnsureUserWebPerms normalises perms for every domain belonging to a user.
// Used by restore/transfer flows where the domain list isn't known upfront.
func EnsureUserWebPerms(ctx context.Context, username string) error {
	domainsDir := fmt.Sprintf("/home/%s/domains", username)
	entries, err := os.ReadDir(domainsDir)
	if err != nil {
		// No domains dir yet — only normalise the home perm.
		_, e := RunCommand(ctx, "chmod", "711", fmt.Sprintf("/home/%s", username))
		return e
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := EnsureWebPerms(ctx, username, e.Name()); err != nil {
			return err
		}
	}
	return nil
}

func SetDiskQuota(ctx context.Context, username string, quotaMB int) error {
	if quotaMB <= 0 {
		return nil
	}
	softLimit := fmt.Sprintf("%dM", quotaMB)
	hardLimit := fmt.Sprintf("%dM", quotaMB)
	_, err := RunCommand(ctx, "setquota", "-u", username, softLimit, hardLimit, "0", "0", "/")
	return err
}

func ServiceAction(ctx context.Context, service, action string) error {
	_, err := RunCommand(ctx, "systemctl", action, service)
	return err
}

func InstallPackages(ctx context.Context, packages ...string) error {
	// Wait for any apt lock to be released before installing
	RunCommand(ctx, "bash", "-c", "while fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1; do sleep 2; done")
	args := append([]string{"install", "-y"}, packages...)
	_, err := RunLongCommand(ctx, "apt-get", args...)
	return err
}

func SetHostname(ctx context.Context, hostname string) error {
	_, err := RunCommand(ctx, "hostnamectl", "set-hostname", hostname)
	return err
}

func SetTimezone(ctx context.Context, tz string) error {
	_, err := RunCommand(ctx, "timedatectl", "set-timezone", tz)
	return err
}

// ReadCrontab returns the raw crontab content for a user. Empty string if none.
func ReadCrontab(ctx context.Context, user string) (string, error) {
	result, err := RunCommand(ctx, "crontab", "-l", "-u", user)
	if err != nil {
		// "no crontab for <user>" exits non-zero — treat as empty, not error.
		return "", nil
	}
	if result == nil {
		return "", nil
	}
	return result.Output, nil
}

// ListCrontabUsers returns the union of "root" and any user that has a crontab
// file in the standard spool directories. Used to discover which users to sync.
func ListCrontabUsers(ctx context.Context) ([]string, error) {
	users := map[string]bool{"root": true}
	for _, dir := range []string{"/var/spool/cron/crontabs", "/var/spool/cron"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && e.Name() != "" {
				users[e.Name()] = true
			}
		}
	}
	out := make([]string, 0, len(users))
	for u := range users {
		out = append(out, u)
	}
	return out, nil
}

func WriteCrontab(ctx context.Context, user, schedule, command string) error {
	entry := fmt.Sprintf("%s %s\n", schedule, command)
	result, _ := RunCommand(ctx, "crontab", "-l", "-u", user)
	existing := ""
	if result != nil {
		existing = result.Output
	}
	existing += entry

	// Write to temp file to avoid shell injection via echo
	tmpFile := fmt.Sprintf("/tmp/crontab_%s_%d", user, os.Getpid())
	if err := os.WriteFile(tmpFile, []byte(existing), 0600); err != nil {
		return fmt.Errorf("failed to write temp crontab: %w", err)
	}
	defer os.Remove(tmpFile)

	_, err := RunCommand(ctx, "crontab", "-u", user, tmpFile)
	return err
}
