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

func RunCommandAsUser(ctx context.Context, user, command string) (*CommandResult, error) {
	return RunCommand(ctx, "sudo", "-u", user, "bash", "-c", command)
}
