package services

import (
	"context"
	"fmt"
	"regexp"
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

// pidPortMap returns a map[pid] -> "[]int of TCP/UDP ports the process is
// LISTENing on". Built from a single `ss -tulnpH` call so the cost is one
// fork even for a 50-process page. Empty when ss isn't installed or the
// process has no listening sockets.
//
// Output line shape (no header):
//
//	tcp  LISTEN  0  511  0.0.0.0:80  0.0.0.0:*  users:(("nginx",pid=123,fd=8))
//
// Multiple `pid=N,fd=M` tuples can appear on one line for forked workers.
func pidPortMap(ctx context.Context) map[string][]int {
	out := map[string][]int{}
	res, err := agent.RunCommand(ctx, "bash", "-c", "ss -tulnpH 2>/dev/null")
	if err != nil || res == nil {
		return out
	}
	pidRe := regexp.MustCompile(`pid=(\d+)`)
	// Match the local-address column's port: ":80" but not ":*"
	portRe := regexp.MustCompile(`:(\d+)\s`)
	for _, line := range strings.Split(res.Output, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		// Local Address:Port is column 5 (1-indexed) for ss -tulnpH
		// but column index varies between tcp/udp output. Find the first
		// "host:port" looking field instead.
		var port int
		for _, f := range fields {
			if i := strings.LastIndex(f, ":"); i >= 0 && i < len(f)-1 {
				if p, perr := strconv.Atoi(f[i+1:]); perr == nil && p > 0 && p < 65536 {
					port = p
					break
				}
			}
		}
		if port == 0 {
			// Fallback regex
			if m := portRe.FindStringSubmatch(line); len(m) > 1 {
				port, _ = strconv.Atoi(m[1])
			}
		}
		if port == 0 {
			continue
		}
		// Every pid=N tuple on the line shares this port
		for _, m := range pidRe.FindAllStringSubmatch(line, -1) {
			pid := m[1]
			// dedupe — udp + tcp on same port shouldn't double-list
			already := false
			for _, p := range out[pid] {
				if p == port {
					already = true
					break
				}
			}
			if !already {
				out[pid] = append(out[pid], port)
			}
		}
	}
	return out
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

	// Single ss call up front, then attach ports per-pid as we iterate the ps
	// output. Cheap enough to run on every List() request — ss takes <20ms
	// for the typical few-hundred-listener box.
	ports := pidPortMap(ctx)

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
		pid := fields[1]
		procPorts := ports[pid]
		if procPorts == nil {
			procPorts = []int{}
		}
		processes = append(processes, map[string]interface{}{
			"user":    fields[0],
			"pid":     pid,
			"cpu":     cpu,
			"memory":  mem,
			"vsz":     vsz,
			"rss":     rss,
			"tty":     fields[6],
			"stat":    fields[7],
			"start":   fields[8],
			"time":    fields[9],
			"command": strings.Join(fields[10:], " "),
			"ports":   procPorts,
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

	// Listening ports for this PID (TCP + UDP). Empty list when nothing is bound.
	if pp := pidPortMap(ctx); pp != nil {
		if ports, ok := pp[pid]; ok {
			info["ports"] = ports
		} else {
			info["ports"] = []int{}
		}
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
