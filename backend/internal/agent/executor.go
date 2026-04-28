package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

type CommandResult struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

func RunCommand(ctx context.Context, name string, args ...string) (*CommandResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &CommandResult{
		Output:   stdout.String(),
		ExitCode: cmd.ProcessState.ExitCode(),
	}
	if err != nil {
		result.Error = stderr.String()
		return result, fmt.Errorf("command failed: %s: %s", err.Error(), stderr.String())
	}
	return result, nil
}

// RunLongCommand is like RunCommand but with a 20-minute timeout for long-running
// operations such as package installations, PPA additions, and apt-get update.
func RunLongCommand(ctx context.Context, name string, args ...string) (*CommandResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &CommandResult{
		Output:   stdout.String(),
		ExitCode: cmd.ProcessState.ExitCode(),
	}
	if err != nil {
		result.Error = stderr.String()
		return result, fmt.Errorf("command failed: %s: %s", err.Error(), stderr.String())
	}
	return result, nil
}

// RunCommandAsUser runs `command` as <user>'s shell. We pass `-H` so sudo
// resets HOME to the target user's home directory. On most modern
// distributions env_reset already does this, but on some shared-hosting
// VPS images sudoers ships with `env_keep += "HOME"` and HOME stays
// /root after sudo -u. wp-cli then tries to write its cache to
// /root/.wp-cli/cache, gets EACCES as the unprivileged user, and the
// whole `wp core download` step fails with a generic exit-1. -H makes
// the behaviour deterministic across every install we ship to.
func RunCommandAsUser(ctx context.Context, user, command string) (*CommandResult, error) {
	return RunCommand(ctx, "sudo", "-H", "-u", user, "bash", "-c", command)
}
