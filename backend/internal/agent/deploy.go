package agent

import (
	"context"
	"fmt"
	"os"
)

func GitClone(ctx context.Context, repoURL, branch, destPath, token string) error {
	if token != "" {
		repoURL = fmt.Sprintf("https://%s@%s", token, repoURL[8:])
	}
	_, err := RunCommand(ctx, "git", "clone", "--depth", "1", "--branch", branch, repoURL, destPath)
	return err
}

func GitPull(ctx context.Context, repoPath, branch string) error {
	_, err := RunCommand(ctx, "git", "-C", repoPath, "pull", "origin", branch)
	return err
}

func CreateSystemdService(ctx context.Context, name, user, workDir, startCmd string, envVars map[string]string) error {
	envLines := ""
	for k, v := range envVars {
		envLines += fmt.Sprintf("Environment=%s=%s\n", k, v)
	}

	unit := fmt.Sprintf(`[Unit]
Description=ServerPanel App - %s
After=network.target

[Service]
Type=simple
User=%s
Group=%s
WorkingDirectory=%s
ExecStart=%s
Restart=always
RestartSec=5
%s
[Install]
WantedBy=multi-user.target
`, name, user, user, workDir, startCmd, envLines)

	serviceName := fmt.Sprintf("sp-app-%s", name)
	path := fmt.Sprintf("/etc/systemd/system/%s.service", serviceName)
	if err := os.WriteFile(path, []byte(unit), 0644); err != nil {
		return err
	}

	RunCommand(ctx, "systemctl", "daemon-reload")
	RunCommand(ctx, "systemctl", "enable", serviceName)
	_, err := RunCommand(ctx, "systemctl", "start", serviceName)
	return err
}

func DeleteSystemdService(ctx context.Context, name string) error {
	serviceName := fmt.Sprintf("sp-app-%s", name)
	RunCommand(ctx, "systemctl", "stop", serviceName)
	RunCommand(ctx, "systemctl", "disable", serviceName)
	os.Remove(fmt.Sprintf("/etc/systemd/system/%s.service", serviceName))
	_, err := RunCommand(ctx, "systemctl", "daemon-reload")
	return err
}

func AtomicSymlinkSwitch(currentLink, newReleasePath string) error {
	tmpLink := currentLink + ".tmp"
	os.Remove(tmpLink)
	if err := os.Symlink(newReleasePath, tmpLink); err != nil {
		return err
	}
	return os.Rename(tmpLink, currentLink)
}
