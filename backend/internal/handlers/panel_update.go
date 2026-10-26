package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/version"
	"github.com/gofiber/fiber/v2"
)

// One-click panel self-update. The apply endpoint launches `bzpanel deploy`
// (git fetch+reset, build ALL binaries + frontend, and only THEN restart
// serverpanel) inside its own transient systemd unit so it survives the
// serverpanel restart it triggers. A failed build never restarts the service,
// so the running panel is never bricked by a bad update.

const (
	panelUpdateSourceDir = "/opt/serverpanel"
	panelUpdateMarker    = "/var/lib/serverpanel/panel-update.json"
	panelUpdateLogDir    = "/var/log/serverpanel"
	// A "running" marker older than this is treated as stale (failed/abandoned).
	panelUpdateStaleAfter = 15 * time.Minute
)

// processStart is when this server process started; used to detect that the
// service restarted after an update was launched (even if the version string
// did not change).
var processStart = time.Now()

var (
	panelApplyMu sync.Mutex // serializes check-marker-then-launch in PanelUpdateApply

	checkMu       sync.Mutex
	checkCache    fiber.Map
	checkCachedAt time.Time
)

const panelCheckTTL = 60 * time.Second

var (
	reURLUserinfo = regexp.MustCompile(`://[^/@\s]*@`)
	reGHToken     = regexp.MustCompile(`ghp_[A-Za-z0-9]+|github_pat_[A-Za-z0-9_]+`)
)

// redactLog strips credential patterns from log text before it leaves the server.
func redactLog(s string) string {
	s = reURLUserinfo.ReplaceAllString(s, "://REDACTED@")
	return reGHToken.ReplaceAllString(s, "REDACTED_TOKEN")
}

// writeMarker writes the marker atomically (tmp + rename) with 0600 perms.
func writeMarker(m panelUpdateMarkerFile) error {
	mb, _ := json.MarshalIndent(m, "", "  ")
	tmp := panelUpdateMarker + ".tmp"
	if err := os.WriteFile(tmp, mb, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, panelUpdateMarker)
}

type panelUpdateMarkerFile struct {
	StartedAt   string `json:"started_at"`
	FromVersion string `json:"from_version"`
	FromCommit  string `json:"from_commit"`
	Status      string `json:"status"`
	LogPath     string `json:"log_path"`
	Unit        string `json:"unit,omitempty"`
}

func gitOut(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", panelUpdateSourceDir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return strings.TrimSpace(string(out)), nil
}

// PanelUpdateCheck compares the local checkout against origin/<branch>.
func (h *MaintenanceHandler) PanelUpdateCheck(c *fiber.Ctx) error {
	// Real `git fetch` at most once per panelCheckTTL; serve the cached result otherwise.
	checkMu.Lock()
	defer checkMu.Unlock()
	if checkCache != nil && time.Since(checkCachedAt) < panelCheckTTL {
		return response.Success(c, checkCache)
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 45*time.Second)
	defer cancel()

	res := fiber.Map{
		"current_version":  version.Number(),
		"update_available": false,
		"behind_count":     0,
	}
	fail := func(msg string) error {
		res["error"] = msg
		return response.Success(c, res) // errors are not cached
	}

	branch, err := gitOut(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fail("Could not read git branch: " + err.Error())
	}
	if branch == "" || branch == "HEAD" {
		branch = "main"
	}
	res["branch"] = branch
	if cur, err := gitOut(ctx, "rev-parse", "--short", "HEAD"); err == nil {
		res["current_commit"] = cur
	}
	if _, err := gitOut(ctx, "fetch", "--quiet", "origin"); err != nil {
		return fail("git fetch failed: " + err.Error())
	}
	latest, err := gitOut(ctx, "rev-parse", "--short", "origin/"+branch)
	if err != nil {
		return fail("Could not resolve origin/" + branch + ": " + err.Error())
	}
	res["latest_commit"] = latest
	behindStr, err := gitOut(ctx, "rev-list", "--count", "HEAD..origin/"+branch)
	if err != nil {
		return fail("Could not compare commits: " + err.Error())
	}
	behind, _ := strconv.Atoi(behindStr)
	res["behind_count"] = behind
	res["update_available"] = behind > 0
	checkCache, checkCachedAt = res, time.Now()
	return response.Success(c, res)
}

func resolveBzpanel() (string, error) {
	if p, err := exec.LookPath("bzpanel"); err == nil {
		return p, nil
	}
	for _, p := range []string{"/usr/local/bin/bzpanel", "/opt/serverpanel/bin/bzpanel"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("bzpanel binary not found")
}

func readPanelUpdateMarker() (*panelUpdateMarkerFile, error) {
	b, err := os.ReadFile(panelUpdateMarker)
	if err != nil {
		return nil, err
	}
	var m panelUpdateMarkerFile
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// PanelUpdateApply launches `bzpanel deploy` in a detached transient systemd
// unit and returns immediately.
//
// TRUST MODEL: this does `git reset --hard origin/<checked-out-branch>` + build +
// run as root. There is NO commit/tag signature check, so push access to the
// origin repo == root on this panel - hence the route is owner-only (vendor_owner).
func (h *MaintenanceHandler) PanelUpdateApply(c *fiber.Ctx) error {
	panelApplyMu.Lock()
	defer panelApplyMu.Unlock()
	// Refuse a concurrent run.
	if m, err := readPanelUpdateMarker(); err == nil && m.Status == "running" {
		if t, perr := time.Parse(time.RFC3339, m.StartedAt); perr == nil &&
			time.Since(t) < panelUpdateStaleAfter && m.FromVersion == version.Number() &&
			!processStart.After(t) {
			return response.Conflict(c, "A panel update is already in progress")
		}
	}

	bz, err := resolveBzpanel()
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	sdRun, err := exec.LookPath("systemd-run")
	if err != nil {
		return response.InternalError(c, "systemd-run not available; run `bzpanel deploy` over SSH instead")
	}

	now := time.Now().UTC()
	ts := now.Format("20060102-150405")
	logPath := filepath.Join(panelUpdateLogDir, "panel-update-"+ts+".log")
	unit := "bzpanel-selfupdate-" + ts

	if err := os.MkdirAll(panelUpdateLogDir, 0o750); err != nil {
		return response.InternalError(c, "create log dir: "+err.Error())
	}
	if err := os.MkdirAll(filepath.Dir(panelUpdateMarker), 0o755); err != nil {
		return response.InternalError(c, "create state dir: "+err.Error())
	}

	fromCommit, _ := gitOut(c.UserContext(), "rev-parse", "--short", "HEAD")
	marker := panelUpdateMarkerFile{
		StartedAt:   now.Format(time.RFC3339),
		FromVersion: version.Number(),
		FromCommit:  fromCommit,
		Status:      "running",
		LogPath:     logPath,
		Unit:        unit,
	}
	if err := writeMarker(marker); err != nil {
		return response.InternalError(c, "write marker: "+err.Error())
	}
	// Pre-create the log 0600 so the detached shell's `>` truncates, not creates, it.
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		return response.InternalError(c, "create log: "+err.Error())
	}

	// Paths are server-controlled constants (no user input reaches the shell).
	script := fmt.Sprintf("umask 077; '%s' deploy > '%s' 2>&1; echo EXIT=$? >> '%s'", bz, logPath, logPath)
	cmd := exec.Command(sdRun,
		"--unit="+unit, "--collect",
		"--description=Betazen panel self-update",
		"/bin/bash", "-lc", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		marker.Status = "failed"
		_ = writeMarker(marker)
		return response.InternalError(c, "failed to launch update: "+strings.TrimSpace(string(out)+" "+err.Error()))
	}

	return response.Success(c, fiber.Map{
		"started":      true,
		"from_version": marker.FromVersion,
		"log_path":     logPath,
		"unit":         unit,
	})
}

func tailFile(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	const maxRead = 64 * 1024
	size := st.Size()
	start := int64(0)
	if size > maxRead {
		start = size - maxRead
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && len(buf) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// PanelUpdateStatus reports progress of the last launched update.
func (h *MaintenanceHandler) PanelUpdateStatus(c *fiber.Ctx) error {
	cur := version.Number()
	m, err := readPanelUpdateMarker()
	if err != nil {
		return response.Success(c, fiber.Map{
			"current_version": cur,
			"running":         false,
			"done":            false,
			"failed":          false,
			"log_tail":        "",
		})
	}
	out := fiber.Map{
		"from_version":    m.FromVersion,
		"current_version": cur,
		"started_at":      m.StartedAt,
		"log_path":        m.LogPath,
	}
	tail := redactLog(tailFile(m.LogPath, 40))
	out["log_tail"] = tail

	started, perr := time.Parse(time.RFC3339, m.StartedAt)
	elapsed := 0
	if perr == nil {
		elapsed = int(time.Since(started).Seconds())
	}
	out["elapsed_seconds"] = elapsed

	// The service restarted after launch (new version, or same version rebuilt).
	restarted := perr == nil && processStart.After(started)
	done := m.Status == "running" && (cur != m.FromVersion || restarted)

	// Exit code recorded by the wrapper (EXIT=n); non-zero means the build failed
	// before any restart, so the old panel is still running untouched.
	exitCode := -1
	for _, ln := range strings.Split(tail, "\n") {
		if strings.HasPrefix(ln, "EXIT=") {
			if v, e := strconv.Atoi(strings.TrimPrefix(ln, "EXIT=")); e == nil {
				exitCode = v
			}
		}
	}
	failed := m.Status == "failed" || (exitCode > 0 && !done)
	running := m.Status == "running" && !done && !failed && perr == nil && time.Since(started) < 10*time.Minute
	if exitCode == 0 && !done {
		// Deploy finished OK but the restart hasn't been observed yet.
		running = perr == nil && time.Since(started) < 10*time.Minute
	}
	out["running"] = running
	out["done"] = done
	out["failed"] = failed
	if exitCode >= 0 {
		out["exit_code"] = exitCode
	}
	return response.Success(c, out)
}
