package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
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

	// systemd requires the first token of ExecStart to be an absolute path.
	// Wrap anything else (relative paths like ./venv/bin/..., shell builtins,
	// `bundle exec ...`, `npm start`, etc.) in a login bash so PATH / HOME
	// are set up and the workDir is respected.
	execStart := startCmd
	trimmed := strings.TrimSpace(startCmd)
	if !strings.HasPrefix(trimmed, "/") {
		// Escape single quotes inside the command before wrapping.
		escaped := strings.ReplaceAll(trimmed, "'", `'\''`)
		execStart = fmt.Sprintf("/bin/bash -lc 'cd %s && %s'", workDir, escaped)
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
`, name, user, user, workDir, execStart, envLines)

	serviceName := fmt.Sprintf("sp-app-%s", name)
	path := fmt.Sprintf("/etc/systemd/system/%s.service", serviceName)
	if err := os.WriteFile(path, []byte(unit), 0644); err != nil {
		return err
	}

	RunCommand(ctx, "systemctl", "daemon-reload")
	RunCommand(ctx, "systemctl", "enable", serviceName)
	// Use restart so that redeploys (new unit file contents, new port, new
	// ExecStart) actually pick up the new definition instead of silently
	// keeping the previously running process around.
	_, err := RunCommand(ctx, "systemctl", "restart", serviceName)
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

// CreateSystemdUnit is the name-explicit variant of CreateSystemdService used
// by the Deploy Software project service, which needs a naming scheme
// ("sp-proj-<slug>-<svc>") distinct from the single-app "sp-app-<name>"
// convention. The unit name must be a valid systemd identifier — the caller
// is responsible for the prefix; this function does no prefixing of its own.
func CreateSystemdUnit(ctx context.Context, unitName, user, workDir, startCmd string, envVars map[string]string) error {
	envLines := ""
	for k, v := range envVars {
		envLines += fmt.Sprintf("Environment=%s=%s\n", k, v)
	}

	execStart := startCmd
	trimmed := strings.TrimSpace(startCmd)
	if !strings.HasPrefix(trimmed, "/") {
		escaped := strings.ReplaceAll(trimmed, "'", `'\''`)
		execStart = fmt.Sprintf("/bin/bash -lc 'cd %s && %s'", workDir, escaped)
	}

	unit := fmt.Sprintf(`[Unit]
Description=ServerPanel Project Service - %s
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
`, unitName, user, user, workDir, execStart, envLines)

	path := fmt.Sprintf("/etc/systemd/system/%s.service", unitName)
	if err := os.WriteFile(path, []byte(unit), 0644); err != nil {
		return err
	}

	RunCommand(ctx, "systemctl", "daemon-reload")
	RunCommand(ctx, "systemctl", "enable", unitName)
	_, err := RunCommand(ctx, "systemctl", "restart", unitName)
	return err
}

// DeleteSystemdUnit is the name-explicit pair for CreateSystemdUnit.
func DeleteSystemdUnit(ctx context.Context, unitName string) error {
	RunCommand(ctx, "systemctl", "stop", unitName)
	RunCommand(ctx, "systemctl", "disable", unitName)
	os.Remove(fmt.Sprintf("/etc/systemd/system/%s.service", unitName))
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
