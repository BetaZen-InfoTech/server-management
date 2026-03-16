package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"go.mongodb.org/mongo-driver/mongo"
)

type ProcessService struct {
	db *mongo.Database
}

func NewProcessService(db *mongo.Database) *ProcessService {
	return &ProcessService{db: db}
}

// List returns a list of running processes sorted by the given field with a limit.
func (s *ProcessService) List(ctx context.Context, sort string, limit int) ([]map[string]interface{}, error) {
	sortField := "cpu"
	switch sort {
	case "memory", "mem":
		sortField = "mem"
	case "pid":
		sortField = "pid"
	default:
		sortField = "cpu"
	}

	cmd := fmt.Sprintf("ps aux --sort=-%%%s | head -n %d", sortField, limit+1)
	result, err := agent.RunCommand(ctx, "bash", "-c", cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to list processes: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(result.Output), "\n")
	var processes []map[string]interface{}
	for i, line := range lines {
		if i == 0 {
			continue // skip header
		}
		fields := strings.Fields(line)
		if len(fields) < 11 {
			continue
		}
		cpu, _ := strconv.ParseFloat(fields[2], 64)
		mem, _ := strconv.ParseFloat(fields[3], 64)
		vsz, _ := strconv.ParseInt(fields[4], 10, 64)
		rss, _ := strconv.ParseInt(fields[5], 10, 64)
		processes = append(processes, map[string]interface{}{
			"user":    fields[0],
			"pid":     fields[1],
			"cpu":     cpu,
			"memory":  mem,
			"vsz":     vsz,
			"rss":     rss,
			"tty":     fields[6],
			"stat":    fields[7],
			"start":   fields[8],
			"time":    fields[9],
			"command": strings.Join(fields[10:], " "),
		})
	}
	if processes == nil {
		processes = []map[string]interface{}{}
	}
	return processes, nil
}

// GetByPID returns detailed information about a specific process.
func (s *ProcessService) GetByPID(ctx context.Context, pid string) (map[string]interface{}, error) {
	// Validate PID is numeric
	if _, err := strconv.Atoi(pid); err != nil {
		return nil, fmt.Errorf("invalid PID")
	}

	info := make(map[string]interface{})
	info["pid"] = pid

	if result, err := agent.RunCommand(ctx, "cat", fmt.Sprintf("/proc/%s/status", pid)); err == nil {
		for _, line := range strings.Split(result.Output, "\n") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				switch key {
				case "Name":
					info["name"] = val
				case "State":
					info["state"] = val
				case "Pid":
					info["pid"] = val
				case "PPid":
					info["ppid"] = val
				case "Uid":
					info["uid"] = strings.Fields(val)[0]
				case "VmRSS":
					info["rss"] = val
				case "VmSize":
					info["vsz"] = val
				case "Threads":
					info["threads"] = val
				}
			}
		}
	} else {
		return nil, fmt.Errorf("process not found")
	}

	if result, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("cat /proc/%s/cmdline | tr '\\0' ' '", pid)); err == nil {
		info["cmdline"] = strings.TrimSpace(result.Output)
	}

	if result, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("ls -la /proc/%s/exe 2>/dev/null | awk '{print $NF}'", pid)); err == nil {
		info["exe"] = strings.TrimSpace(result.Output)
	}

	return info, nil
}

// Kill sends a signal to a process.
func (s *ProcessService) Kill(ctx context.Context, pid string, signal string) error {
	if _, err := strconv.Atoi(pid); err != nil {
		return fmt.Errorf("invalid PID")
	}

	allowedSignals := map[string]bool{
		"SIGTERM": true, "SIGKILL": true, "SIGHUP": true,
		"SIGINT": true, "SIGUSR1": true, "SIGUSR2": true,
		"9": true, "15": true,
	}
	if !allowedSignals[signal] {
		return fmt.Errorf("signal not allowed: %s", signal)
	}

	_, err := agent.RunCommand(ctx, "kill", fmt.Sprintf("-%s", signal), pid)
	if err != nil {
		return fmt.Errorf("failed to kill process %s: %w", pid, err)
	}
	return nil
}

// ListServices returns the status of all managed systemd services.
func (s *ProcessService) ListServices(ctx context.Context) ([]map[string]interface{}, error) {
	result, err := agent.RunCommand(ctx, "systemctl", "list-units", "--type=service", "--all", "--no-pager", "--plain")
	if err != nil {
		return nil, fmt.Errorf("failed to list services: %w", err)
	}

	var services []map[string]interface{}
	lines := strings.Split(result.Output, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.HasSuffix(fields[0], ".service") {
			continue
		}
		name := strings.TrimSuffix(fields[0], ".service")
		services = append(services, map[string]interface{}{
			"name":        name,
			"load":        fields[1],
			"active":      fields[2],
			"sub":         fields[3],
			"description": strings.Join(fields[4:], " "),
		})
	}
	if services == nil {
		services = []map[string]interface{}{}
	}
	return services, nil
}

// ControlService performs an action (start, stop, restart, enable, disable) on a service.
func (s *ProcessService) ControlService(ctx context.Context, name string, action string) error {
	allowedActions := map[string]bool{
		"start": true, "stop": true, "restart": true, "enable": true, "disable": true,
	}
	if !allowedActions[action] {
		return fmt.Errorf("action not allowed: %s", action)
	}

	allowedServices := map[string]bool{
		"nginx": true, "mongod": true, "postfix": true, "dovecot": true,
		"fail2ban": true, "ufw": true,
	}
	// Also allow php-fpm and sp-app services
	if !allowedServices[name] && !strings.HasPrefix(name, "php") && !strings.HasPrefix(name, "sp-app-") {
		return fmt.Errorf("service not allowed: %s", name)
	}

	return agent.ServiceAction(ctx, name, action)
}
