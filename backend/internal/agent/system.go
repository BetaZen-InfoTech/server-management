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

func DeleteLinuxUser(ctx context.Context, username string) error {
	_, err := RunCommand(ctx, "userdel", "-r", username)
	return err
}

func CreateUserDirectories(ctx context.Context, username string) error {
	dirs := []string{"public_html", "logs", "tmp", "apps", "backups", "ssl"}
	for _, d := range dirs {
		path := fmt.Sprintf("/home/%s/%s", username, d)
		os.MkdirAll(path, 0755)
	}
	defaultHTML := `<!DOCTYPE html><html><head><title>Welcome</title></head><body><h1>Welcome to your new website!</h1></body></html>`
	os.WriteFile(fmt.Sprintf("/home/%s/public_html/index.html", username), []byte(defaultHTML), 0644)
	_, err := RunCommand(ctx, "chown", "-R", username+":"+username, fmt.Sprintf("/home/%s", username))
	return err
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
	args := append([]string{"install", "-y"}, packages...)
	_, err := RunCommand(ctx, "apt-get", args...)
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

func WriteCrontab(ctx context.Context, user, schedule, command string) error {
	entry := fmt.Sprintf("%s %s\n", schedule, command)
	result, _ := RunCommand(ctx, "crontab", "-l", "-u", user)
	existing := ""
	if result != nil {
		existing = result.Output
	}
	existing += entry
	_, err := RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo '%s' | crontab -u %s -", existing, user))
	return err
}
