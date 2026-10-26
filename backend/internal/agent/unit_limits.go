package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Raised fd/task ceilings for deployed apps and nginx. High-concurrency apps
// (e.g. a WhatsApp campaign bot) exhaust the default ~1024 fds -> EMFILE ->
// the API freezes under load.
//
// LimitNOFILE is soft:hard — soft 65536 fixes EMFILE, hard 1048576 keeps
// headroom (soft=hard=nr_open leaves zero margin). TasksMax is finite (16384):
// generous for worker-thread heavy apps yet still bounds a fork bomb.
const (
	unitFDSoft       = 65536
	unitFDHard       = 1048576
	unitTasksMax     = 16384
	nginxConfPath    = "/etc/nginx/nginx.conf"
	nginxRlimitValue = 1048576
)

var nginxRlimitRe = regexp.MustCompile(`(?m)^([ \t]*worker_rlimit_nofile[ \t]+)(\d+)([ \t]*;)`)
var nginxWorkerProcRe = regexp.MustCompile(`(?m)^[ \t]*worker_processes[^\n]*\n`)

// unitHardLimit returns the hard fd value for the drop-in, clamped to
// fs.nr_open so a unit can never be made unstartable on a tuned kernel.
func unitHardLimit() int {
	hard := unitFDHard
	if b, err := os.ReadFile("/proc/sys/fs/nr_open"); err == nil {
		if n, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil && n > 0 && n < hard {
			hard = n
		}
	}
	return hard
}

// UnitLimitsResult summarises EnsureUnitLimits.
type UnitLimitsResult struct {
	Healed    int // drop-ins written
	Restarted int // units restarted (opt-in only)
	Skipped   int // restart skipped: already at target, not running, or failed/activating
	Failed    int
	Messages  []string
}

// EnsureUnitLimits writes a non-destructive systemd drop-in
// (<unit>.service.d/limits.conf) for every panel-managed app unit
// (sp-proj-*, sp-app-*) and daemon-reloads. With restart=false (the default
// everywhere) NOTHING is restarted: the limits apply on each app's next
// normal restart/redeploy. With restart=true (explicit opt-in; interrupts
// running apps) only running units whose EFFECTIVE LimitNOFILE is still below
// target are restarted; failed/activating units and already-fixed units are
// skipped. Unit files are never rewritten. Idempotent, best-effort per unit.
func EnsureUnitLimits(ctx context.Context, restart bool) (*UnitLimitsResult, error) {
	res := &UnitLimitsResult{}
	hard := unitHardLimit()
	dropIn := fmt.Sprintf("[Service]\nLimitNOFILE=%d:%d\nTasksMax=%d\n", unitFDSoft, hard, unitTasksMax)

	var units []string
	for _, pat := range []string{"sp-proj-*.service", "sp-app-*.service"} {
		m, _ := filepath.Glob(filepath.Join("/etc/systemd/system", pat))
		units = append(units, m...)
	}
	var written []string
	for _, p := range units {
		unit := filepath.Base(p)
		dir := p + ".d"
		if err := os.MkdirAll(dir, 0755); err != nil {
			res.Failed++
			res.Messages = append(res.Messages, fmt.Sprintf("%s: mkdir drop-in: %v", unit, err))
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, "limits.conf"), []byte(dropIn), 0644); err != nil {
			res.Failed++
			res.Messages = append(res.Messages, fmt.Sprintf("%s: write drop-in: %v", unit, err))
			continue
		}
		written = append(written, unit)
	}
	res.Healed = len(written)
	if len(written) == 0 {
		return res, nil
	}
	if _, err := RunCommand(ctx, "systemctl", "daemon-reload"); err != nil {
		return res, fmt.Errorf("daemon-reload: %w", err)
	}
	if !restart {
		return res, nil
	}
	for _, unit := range written {
		state := ""
		if r, _ := RunCommand(ctx, "systemctl", "is-active", unit); r != nil {
			state = strings.TrimSpace(r.Output)
		}
		if state != "active" { // inactive / failed / activating: never touch
			res.Skipped++
			continue
		}
		if r, _ := RunCommand(ctx, "systemctl", "show", "-p", "LimitNOFILE", "--value", unit); r != nil {
			cur := strings.TrimSpace(r.Output)
			if cur == "infinity" {
				res.Skipped++
				continue
			}
			if n, perr := strconv.ParseInt(cur, 10, 64); perr == nil && n >= int64(unitFDSoft) {
				res.Skipped++ // already at/above target: never bounce a fixed app
				continue
			}
		}
		_, _ = RunCommand(ctx, "systemctl", "reset-failed", unit)
		if _, err := RunCommand(ctx, "systemctl", "restart", unit); err != nil {
			res.Failed++
			res.Messages = append(res.Messages, fmt.Sprintf("%s: restart failed: %v (limits apply on next start)", unit, err))
			continue
		}
		res.Restarted++
	}
	return res, nil
}

// EnsureNginxFDLimit idempotently makes sure /etc/nginx/nginx.conf carries
// `worker_rlimit_nofile 1048576;` (inserted after worker_processes, else at
// the top; an existing lower value is raised, an equal/higher one is left).
// The write is atomic (temp file + rename) after an on-disk timestamped
// backup, and nginx -t guarded: on a failed test the original is restored.
// Returns true if the file was changed + reloaded.
//
// Known limitation: nginx.conf is also written by EnsureNginxFileLimits and
// ConfigService.UpdateNginx; there is no cross-path mutex (manual-ops race).
func EnsureNginxFDLimit(ctx context.Context) (bool, error) {
	orig, err := os.ReadFile(nginxConfPath)
	if err != nil {
		return false, fmt.Errorf("read nginx.conf: %w", err)
	}
	src := string(orig)
	line := fmt.Sprintf("worker_rlimit_nofile %d;", nginxRlimitValue)
	var out string
	if m := nginxRlimitRe.FindStringSubmatch(src); m != nil {
		if n, _ := strconv.Atoi(m[2]); n >= nginxRlimitValue {
			return false, nil
		}
		out = nginxRlimitRe.ReplaceAllString(src, "${1}"+strconv.Itoa(nginxRlimitValue)+"${3}")
	} else if loc := nginxWorkerProcRe.FindStringIndex(src); loc != nil {
		out = src[:loc[1]] + line + "\n" + src[loc[1]:]
	} else {
		out = line + "\n" + src
	}

	bak := fmt.Sprintf("%s.bak-%s", nginxConfPath, time.Now().Format("20060102-150405"))
	if err := os.WriteFile(bak, orig, 0644); err != nil {
		return false, fmt.Errorf("backup nginx.conf: %w", err)
	}
	if err := atomicWriteFile(nginxConfPath, []byte(out), 0644); err != nil {
		return false, fmt.Errorf("write nginx.conf: %w", err)
	}
	if _, err := RunCommand(ctx, "nginx", "-t"); err != nil {
		_ = atomicWriteFile(nginxConfPath, orig, 0644)
		return false, fmt.Errorf("nginx -t failed, nginx.conf restored (backup %s): %w", bak, err)
	}
	if _, err := RunCommand(ctx, "systemctl", "reload", "nginx"); err != nil {
		return true, fmt.Errorf("nginx reload: %w", err)
	}
	return true, nil
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp-fdlimit"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
