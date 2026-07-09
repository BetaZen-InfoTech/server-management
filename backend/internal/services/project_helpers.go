package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// serviceWorkDir returns the directory where install/build/start commands
// should run for a project service. install_dir is always the repo root
// (so .git lives there and `git pull` works on every redeploy). When
// GitSubpath is set, the actual app code lives at install_dir/<subpath>
// and that's where commands need to execute. With no subpath, app code IS
// at install_dir.
//
// This replaces the older "strip the subpath out of the clone" approach,
// which left .git behind and forced every redeploy to be a full re-clone
// instead of a fast `git pull`.
func serviceWorkDir(installDir, subpath string) string {
	sub := strings.Trim(strings.TrimSpace(subpath), "/")
	if sub == "" {
		return installDir
	}
	return filepath.Join(installDir, sub)
}

// diagnoseStartCrash inspects the last lines of a service's journalctl output
// and returns an actionable hint when it recognises a common failure pattern.
// Returns "" when nothing matches — the operator still gets the raw journal
// to debug from.
func diagnoseStartCrash(journal string) string {
	if journal == "" {
		return ""
	}
	low := strings.ToLower(journal)
	// "sh: 1: cross-env: not found" / "command not found" — almost always a
	// dev-dependency that didn't install. We now force NPM_CONFIG_PRODUCTION=
	// false so a re-Install fixes it; tell the operator to click the button.
	if strings.Contains(low, "not found") && (strings.Contains(low, "cross-env") || strings.Contains(low, "ts-node") || strings.Contains(low, "tsx") || strings.Contains(low, "nodemon") || strings.Contains(low, "nest") || strings.Contains(low, "vite") || strings.Contains(low, "next:")) {
		return "Hint: a CLI from devDependencies isn't installed. Click the Install (📦) button to re-run npm install — devDependencies are now forced on, so the missing tool will be picked up."
	}
	if strings.Contains(low, "cannot find module") {
		return "Hint: a Node module is missing from node_modules. Click Install (📦) to re-run npm install, then Run (▶) — make sure the module is listed in package.json."
	}
	if strings.Contains(low, "address already in use") || strings.Contains(low, "eaddrinuse") {
		return "Hint: another process is already listening on this port. Stop the conflicting service or change the port via Edit (✎)."
	}
	if strings.Contains(low, "permission denied") {
		return "Hint: the service user doesn't have permission to read or execute something in the install directory. Check file ownership (chown) under the project's user."
	}
	if strings.Contains(low, "syntaxerror") || strings.Contains(low, "unexpected token") {
		return "Hint: the start file has a syntax/parse error. Check that Build succeeded and the entrypoint is correct."
	}
	return ""
}

// tailJournal returns the last `lines` lines from the systemd journal for a
// unit. Used to give the operator an actionable error summary when a deploy's
// health-check step fails because the service crash-looped instead of binding
// its port. Returns an empty string on any error so callers can simply
// concatenate the result without nil checks.
func tailJournal(ctx context.Context, unitName string, lines int) string {
	if unitName == "" || lines <= 0 {
		return ""
	}
	res, err := agent.RunCommand(ctx, "journalctl", "-u", unitName, "-n", strconv.Itoa(lines), "--no-pager")
	if err != nil || res == nil {
		return ""
	}
	out := strings.TrimSpace(res.Output)
	// Cap total length so a runaway log line doesn't blow up the
	// deployment record + UI render. 4 KB is plenty to spot the cause.
	const maxBytes = 4096
	if len(out) > maxBytes {
		out = "...(truncated)...\n" + out[len(out)-maxBytes:]
	}
	return out
}

// detectListeningPort returns the TCP port the given systemd unit is bound
// to. Two-tier strategy:
//
//  1. FAST PATH — if `expectedPort` > 0 and anything (anywhere on the box)
//     is listening on that exact port, return it. Justification: the caller
//     allocated the port via allocatePort() right before launching the unit
//     (port is verified free at that moment). The chance of a foreign
//     process snapping the same port in the few seconds before our app
//     binds it is effectively zero. Skipping pid validation here is what
//     made the false "start step failed" stop happening for apps whose
//     listener is in a deep grandchild (Next.js 16 dispatcher, Vite
//     preview, gunicorn workers, ...).
//
//  2. FALLBACK — if no expected port given, OR the expected port isn't
//     bound, walk the unit's cgroup.procs and look for ANY listener
//     pid=N where N is in the cgroup. Catches apps that hardcode a
//     different port and ignore PORT env.
//
// Returns 0 only when both paths give up within `timeout`.
//
// Motivation: apps that hardcode a listen port otherwise end up behind a
// reverse-proxy vhost pointing at a port nothing is listening on (502 Bad
// Gateway). The previous implementation also falsely flagged Next.js
// services as failed because the listener pid wasn't yet in cgroup.procs
// when ss snapshot was taken.
func detectListeningPort(ctx context.Context, unitName string, timeout time.Duration, expectedPort ...int) int {
	exp := 0
	if len(expectedPort) > 0 {
		exp = expectedPort[0]
	}
	deadline := time.Now().Add(timeout)
	cgroup := fmt.Sprintf("/system.slice/%s.service", unitName)

	cgroupPids := func() map[string]struct{} {
		res, err := agent.RunCommand(ctx, "bash", "-c",
			fmt.Sprintf(`cat /sys/fs/cgroup%s/cgroup.procs 2>/dev/null || cat /sys/fs/cgroup/systemd%s/cgroup.procs 2>/dev/null`, cgroup, cgroup))
		if err != nil || res == nil {
			return nil
		}
		out := map[string]struct{}{}
		for _, line := range strings.Split(res.Output, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				out[line] = struct{}{}
			}
		}
		return out
	}

	for time.Now().Before(deadline) {
		// FAST PATH: the expected port is bound by anything at all.
		// Cheapest possible check — single ss filtered by port.
		if exp > 0 {
			res, err := agent.RunCommand(ctx, "bash", "-c",
				fmt.Sprintf(`ss -ltnH 2>/dev/null | awk '{print $4}' | awk -F: '{print $NF}' | grep -Fx %d`, exp))
			if err == nil && res != nil && strings.TrimSpace(res.Output) != "" {
				return exp
			}
		}

		// FALLBACK: cgroup-pid match. Used when the caller didn't pass
		// expectedPort, or the app bound to a different port than we
		// allocated (hardcoded port that ignores PORT env var).
		ssRes, err := agent.RunCommand(ctx, "bash", "-c", `ss -ltnpH 2>/dev/null`)
		if err == nil && ssRes != nil {
			pidSet := cgroupPids()
			if len(pidSet) > 0 {
				for _, line := range strings.Split(ssRes.Output, "\n") {
					if line == "" {
						continue
					}
					// Pick the local address column (first field with ':' that
					// isn't the users: tuple).
					var addr string
					for _, f := range strings.Fields(line) {
						if strings.Contains(f, ":") && !strings.HasPrefix(f, "users:") {
							addr = f
						}
					}
					if addr == "" {
						continue
					}
					colon := strings.LastIndex(addr, ":")
					if colon < 0 {
						continue
					}
					port, perr := strconv.Atoi(addr[colon+1:])
					if perr != nil || port <= 0 {
						continue
					}
					// Match any pid=N on the line against the cgroup pid set.
					start := 0
					for {
						p := strings.Index(line[start:], "pid=")
						if p < 0 {
							break
						}
						p += start
						end := p + 4
						for end < len(line) && line[end] >= '0' && line[end] <= '9' {
							end++
						}
						if _, ok := pidSet[line[p+4:end]]; ok {
							return port
						}
						start = end
					}
				}
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
	return 0
}

// readGitHeadCommit returns the latest commit info from a cloned repo,
// or nil when the directory isn't a git repo / git isn't available. Used
// by ProjectActivity so the WHM UI can show "Last commit: a7f3b2c — Fix
// login bug — by Sayantan — 3h ago" without a separate git endpoint.
func readGitHeadCommit(ctx context.Context, installDir string) *CommitInfo {
	res, err := agent.RunCommand(ctx, "git", "-C", installDir, "log", "-1",
		"--pretty=format:%H%n%s%n%an%n%cI")
	if err != nil || res == nil {
		return nil
	}
	lines := strings.SplitN(strings.TrimSpace(res.Output), "\n", 4)
	if len(lines) < 4 {
		return nil
	}
	sha := strings.TrimSpace(lines[0])
	short := sha
	if len(short) > 7 {
		short = short[:7]
	}
	t, _ := time.Parse(time.RFC3339, strings.TrimSpace(lines[3]))
	return &CommitInfo{
		SHA:     sha,
		Short:   short,
		Message: strings.TrimSpace(lines[1]),
		Author:  strings.TrimSpace(lines[2]),
		Date:    t,
	}
}

// readSystemdStats queries systemd for a single service's runtime
// metrics (active/inactive, MainPID, uptime, RSS, restart count). Used
// by ProjectActivity. Failures are silent — returns whatever fields we
// could fill so the UI can render partial info gracefully.
func readSystemdStats(ctx context.Context, svc models.ProjectService) ServiceRuntimeStats {
	out := ServiceRuntimeStats{ServiceID: svc.ID.Hex(), Name: svc.Name, Status: svc.Status}
	res, err := agent.RunCommand(ctx, "systemctl", "show", svc.SystemdUnit,
		"-p", "ActiveState",
		"-p", "MainPID",
		"-p", "ActiveEnterTimestamp",
		"-p", "MemoryCurrent",
		"-p", "NRestarts")
	if err != nil || res == nil {
		return out
	}
	for _, line := range strings.Split(res.Output, "\n") {
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		k := line[:eq]
		v := strings.TrimSpace(line[eq+1:])
		switch k {
		case "ActiveState":
			out.UnitState = v
		case "MainPID":
			out.MainPID = v
		case "ActiveEnterTimestamp":
			if v != "" && v != "n/a" {
				if t, perr := time.Parse("Mon 2006-01-02 15:04:05 MST", v); perr == nil {
					out.UptimeSec = int64(time.Since(t).Seconds())
					if out.UptimeSec < 0 {
						out.UptimeSec = 0
					}
				}
			}
		case "MemoryCurrent":
			if n, perr := strconv.ParseInt(v, 10, 64); perr == nil && n > 0 {
				out.MemoryMB = n / (1024 * 1024)
			}
		case "NRestarts":
			if n, perr := strconv.Atoi(v); perr == nil {
				out.NumRestarts = n
			}
		}
	}
	return out
}

// pluralS returns "s" when n != 1, "" otherwise. Tiny helper so error
// messages don't read "1 keys" / "0 vars".
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// requiredEnvKeysFromExample inspects the just-cloned repo for an
// .env.example (or .env.sample / .env.template) and returns the list of
// keys the operator hasn't supplied via env_vars yet. Empty slice means
// "no .env.example present, OR every documented key was supplied".
//
// Used by Provision/AddService as a pre-flight: refusing to start a
// service whose .env.example declares MONGODB_URI / JWT_SECRET / etc.
// when env_vars is empty saves the operator from a confusing "start
// step failed" with a mongoose stack trace.
//
// Parser is intentionally tolerant — handles `KEY=value`, `KEY=`,
// `KEY = "value"`, comments (#) and blank lines. Doesn't try to validate
// values; only the KEY name on the left of the first '=' matters.
func requiredEnvKeysFromExample(installDir string, supplied map[string]string) []string {
	candidates := []string{".env.example", ".env.sample", ".env.template"}
	var raw []byte
	for _, name := range candidates {
		p := filepath.Join(installDir, name)
		if data, err := os.ReadFile(p); err == nil {
			raw = data
			break
		}
	}
	if len(raw) == 0 {
		return nil
	}
	keys := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		// Strip a leading "export " (common in .env.example files meant
		// to be sourced).
		key = strings.TrimPrefix(key, "export ")
		key = strings.TrimSpace(key)
		// Reject keys that don't look like env var names (alpha/digit/_,
		// must start with letter or _). This prevents picking up garbage
		// from heredocs / comments split mid-line.
		if !isEnvKey(key) || seen[key] {
			continue
		}
		// If the operator already supplied a value (even an empty
		// string is fine — they made the choice), don't flag it.
		if _, ok := supplied[key]; ok {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	return keys
}

// isEnvKey reports whether s is a valid POSIX shell variable name
// (matches what dotenv / systemd EnvironmentFile actually parse).
func isEnvKey(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// softDeleteDir renames a directory to "<dir>.<suffix>-<unix-timestamp>"
// so the user's code is preserved and visible in File Manager instead of
// being permanently removed. No-op when the source path doesn't exist.
//
// Used by failure / removal paths in the Deploy Software flow per the
// operator's "do not auto-delete any file/folder" rule. A future cleanup
// job (or the operator via File Manager) can sweep stale .failed-* /
// .deleted-* dirs when convenient.
func softDeleteDir(path, suffix string) {
	path = strings.TrimRight(path, "/")
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err != nil {
		return
	}
	target := fmt.Sprintf("%s.%s-%d", path, suffix, time.Now().Unix())
	_ = os.Rename(path, target)
}

var serviceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)
var envVarKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var branchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)

// validateServiceName keeps names safe for systemd unit files, shell
// interpolation, and MongoDB unique-index compound keys.
func validateServiceName(name string) error {
	if !serviceNamePattern.MatchString(name) {
		return fmt.Errorf("invalid service name %q: must be 2-32 chars, lowercase, start with a letter, only a-z 0-9 '-' and '_'", name)
	}
	return nil
}

// validateGitSubpath rejects anything that would let a malicious request
// reach outside the cloned tempdir (path traversal). Normalises the input
// via filepath.Clean and requires the result to be a plain relative path.
// An empty subpath is legal and means "repo root".
func validateGitSubpath(sub string) (string, error) {
	sub = strings.TrimSpace(sub)
	sub = strings.Trim(sub, "/")
	if sub == "" {
		return "", nil
	}
	// Reject absolute paths and null bytes outright.
	if strings.Contains(sub, "\x00") || strings.HasPrefix(sub, "/") {
		return "", fmt.Errorf("invalid subpath %q", sub)
	}
	// Clean the path then verify it hasn't escaped by checking for .. and
	// that the cleaned form is still relative. filepath.Clean("a/../..")
	// returns "..", which we reject.
	clean := filepath.Clean(sub)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "..\\") || strings.Contains(clean, "/../") {
		return "", fmt.Errorf("subpath %q must not escape repo root", sub)
	}
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("subpath %q must be relative", sub)
	}
	return clean, nil
}

// validateEnvVars rejects keys that would be interpreted by a shell when the
// values are written to a .env file or passed to systemd Environment= lines.
// POSIX says env names are `[A-Za-z_][A-Za-z0-9_]*`; anything else could be
// a command-injection vector.
func validateEnvVars(env map[string]string) error {
	for k := range env {
		if !envVarKeyPattern.MatchString(k) {
			return fmt.Errorf("invalid env var key %q: must match [A-Za-z_][A-Za-z0-9_]*", k)
		}
	}
	return nil
}

// validateBranch rejects branch names that aren't valid git refs. Real git
// allows a lot more (e.g. `.`, `@{}`) but we only need a safe subset for the
// webhook branch comparison and shell interpolation in `git pull origin X`.
func validateBranch(branch string) error {
	if !branchPattern.MatchString(branch) {
		return fmt.Errorf("invalid branch %q", branch)
	}
	return nil
}

// sanitiseGitError strips the plaintext PAT from a git command's error
// message before it ends up in an HTTP response, a toast, or an audit log.
// git itself sometimes echoes the full URL (token included) when
// authentication fails — leaking it to every log reader is unacceptable.
// Empty token is a no-op.
func sanitiseGitError(err error, token string) string {
	msg := err.Error()
	if token != "" {
		msg = strings.ReplaceAll(msg, token, "***")
	}
	return msg
}

// sanitiseGitURL rewrites a token-in-URL form ("https://<token>@host/path")
// back to its credential-free shape for logging purposes. The cloned repo
// on disk still has the token baked into its origin URL (that's necessary
// for pulls), but we never want to commit the token-full form to a log file.
func sanitiseGitURL(url string) string {
	const prefix = "https://"
	if !strings.HasPrefix(url, prefix) {
		return url
	}
	rest := url[len(prefix):]
	at := strings.Index(rest, "@")
	slash := strings.Index(rest, "/")
	if at < 0 || (slash >= 0 && at > slash) {
		return url
	}
	return prefix + rest[at+1:]
}

// ansiRe matches the common CSI ANSI escape sequences emitted by npm, next,
// gcc, and friends when they detect a TTY (or honor FORCE_COLOR). We strip
// them before surfacing build output in HTTP responses so error toasts
// don't render as "\u001b[90m170 |\u001b[0m" soup.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// stripANSI returns s with all CSI ANSI escape sequences removed. Safe to
// call on any string (no-op when there are no escapes).
func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// noColorPrefix is prepended to every install / build command so tools that
// honor the widely-supported NO_COLOR / FORCE_COLOR / CI env vars emit
// plain text in the first place. Stripping ANSI afterwards still runs as a
// belt-and-suspenders layer in case an old tool ignores these.
const noColorPrefix = "NO_COLOR=1 FORCE_COLOR=0 CI=1 "

// withNoColor prepends the env-var prefix to a shell command. Returns the
// original string when cmd is empty so the caller's "skip if empty" logic
// keeps working without an extra guard.
func withNoColor(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	return noColorPrefix + cmd
}

// BuildError is the typed error returned from a build/install step so the
// HTTP handler can map it to 422 Unprocessable Entity (user-code problem)
// instead of 500 Internal Server Error (our infra broke). Keeps the raw
// build output (ANSI-stripped) in Details for the UI to render in a
// preformatted block.
type BuildError struct {
	Stage   string // "install" | "build"
	Summary string // one-line human summary
	Details string // full ANSI-stripped output for the details modal
}

func (e *BuildError) Error() string {
	if e.Summary != "" {
		return e.Stage + " failed: " + e.Summary
	}
	return e.Stage + " failed"
}

// extractNodeEntry returns the entry file referenced by a `node X` style
// start command, or "" if the command isn't a direct node invocation.
// Handles the common forms:
//
//	node server.js
//	node ./server.js
//	node --experimental-vm-modules src/main.js
//	nodejs dist/index.cjs
//	/usr/local/bin/node app.mjs
//
// Used to pre-flight check that the file actually exists in the cloned
// repo — a mismatch would otherwise land as a systemd crash-loop and
// nginx 502. Tokenising the command by whitespace is simpler and more
// reliable than trying to do it all in a single regex: the earlier
// attempt used `nodejs?\b` which matches "nodejs" OR "nodej", not
// "node" OR "nodejs" as intended, so `node server.js` never triggered.
func extractNodeEntry(cmd string) string {
	fields := strings.Fields(strings.TrimSpace(cmd))
	if len(fields) == 0 {
		return ""
	}
	exe := fields[0]
	// Strip any leading absolute/relative path on the executable name.
	if idx := strings.LastIndex(exe, "/"); idx >= 0 {
		exe = exe[idx+1:]
	}
	if exe != "node" && exe != "nodejs" {
		return ""
	}
	// Skip any flag arguments (anything starting with '-'). Also skip
	// `-r pkg` / `--require pkg` style arg-taking flags.
	for i := 1; i < len(fields); i++ {
		arg := fields[i]
		if strings.HasPrefix(arg, "-") {
			// Flags with a separate value argument we should also skip.
			// --flag=val takes no extra slot; plain "-r pkg" does.
			noEq := !strings.Contains(arg, "=")
			if noEq && (arg == "-r" || arg == "--require" || arg == "--import" || arg == "-e" || arg == "--eval" || arg == "-p" || arg == "--print" || arg == "--inspect-brk" || arg == "--inspect") {
				i++ // skip the value
			}
			continue
		}
		if strings.HasSuffix(arg, ".js") || strings.HasSuffix(arg, ".mjs") || strings.HasSuffix(arg, ".cjs") || strings.HasSuffix(arg, ".ts") {
			return strings.TrimPrefix(arg, "./")
		}
		// First positional arg that isn't a JS file → not a direct node
		// entry invocation (could be `node inspect <script>`, `node run <task>`,
		// etc.). Bail.
		return ""
	}
	return ""
}

// fetchUnitJournal grabs the last N log lines from a systemd unit, stripped
// of timestamps, for surfacing in a BuildError when a just-started backend
// service fails to bind any port. Without this, a crash-on-start is invisible
// to the operator — they see a generic "no process listening" failure and
// have to SSH in to find the actual reason.
func fetchUnitJournal(ctx context.Context, unitName string, lines int) string {
	if unitName == "" {
		return ""
	}
	res, err := agent.RunCommand(ctx, "journalctl", "-u", unitName, "-n", strconv.Itoa(lines), "--no-pager", "--output=cat")
	if err != nil || res == nil {
		return ""
	}
	return stripANSI(strings.TrimSpace(res.Output))
}

// buildErrorFrom wraps a raw runBuildAsUser error in a typed BuildError with
// ANSI-stripped details and a one-line summary. The handler layer uses the
// *BuildError type to map these failures to HTTP 422 instead of 500 — a
// failed build is a problem with the user's code, not with our server.
func buildErrorFrom(stage string, err error) *BuildError {
	if err == nil {
		return nil
	}
	clean := stripANSI(err.Error())
	return &BuildError{
		Stage:   stage,
		Summary: summariseBuildOutput(clean),
		Details: clean,
	}
}

// summariseBuildOutput extracts the most useful one-line error from a multi-
// line build log. Prefers lines like "Type error:", "SyntaxError:", or
// "error TS2304:" since those are the actual root-cause lines in typical
// TypeScript / Next.js / webpack output.
func summariseBuildOutput(output string) string {
	lines := strings.Split(output, "\n")
	patterns := []string{
		"Type error:",
		"SyntaxError",
		"ReferenceError",
		"ERR_",
		"error TS",
		"ENOENT",
		"EACCES",
		"Module not found",
		"Cannot find module",
		"Cannot find name",
		"npm ERR!",
		"fatal:",
	}
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			continue
		}
		for _, p := range patterns {
			if strings.Contains(trimmed, p) {
				if len(trimmed) > 200 {
					trimmed = trimmed[:200] + "…"
				}
				return trimmed
			}
		}
	}
	// Nothing matched — fall back to the last non-empty line, which tends
	// to be the failing tool's own error summary.
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			if len(t) > 200 {
				t = t[:200] + "…"
			}
			return t
		}
	}
	return ""
}

// defaultProjectUser builds the Linux username for a project when the caller
// hasn't picked one. Prefixed with 'sp-' so it's obvious in /etc/passwd that
// the panel created the account. We keep the first 20 chars of the slug (so
// the account is still recognisable in ps output) and append a 4-char hex
// digest of the full slug so two slugs that share a 20-char prefix resolve
// to different users. Total length stays comfortably under Debian's 32-char
// NAME_REGEX limit.
func defaultProjectUser(slug string) string {
	prefix := slug
	if len(prefix) > 20 {
		prefix = prefix[:20]
	}
	sum := sha256.Sum256([]byte(slug))
	hash := hex.EncodeToString(sum[:2]) // 4 hex chars = 16 bits, plenty
	return "sp-" + prefix + "-" + hash
}

// roleToAppType bridges the ProjectService.Role taxonomy (backend/frontend/
// static) to the AppType names the existing preset + runtime-resolver helpers
// expect.
func roleToAppType(role string) string {
	switch role {
	case "backend":
		return "node"
	case "frontend", "static":
		return "static"
	default:
		return role
	}
}

// resolveServiceAppType picks the language bucket (node/python/ruby/go/static)
// a project service belongs to. Framework wins when it maps to a preset —
// e.g. a "backend" role with Framework "go-gin" should resolve to "go", not
// "node" (the default roleToAppType returns for backends). Role-only fallback
// preserves behaviour for custom/unknown frameworks.
func resolveServiceAppType(framework, role string) string {
	if p, ok := lookupPreset(framework); ok && p.AppType != "" {
		return p.AppType
	}
	return roleToAppType(role)
}

// collectUsedPorts scans the existing apps AND project services so a new
// service doesn't collide with a port already owned by a single-App deploy.
func collectUsedPorts(ctx context.Context, db *mongo.Database) map[int]bool {
	used := map[int]bool{}
	for _, col := range []string{database.ColApps, database.ColProjectServices} {
		cur, err := db.Collection(col).Find(ctx, bson.M{"port": bson.M{"$gt": 0}})
		if err != nil {
			continue
		}
		var rows []struct {
			Port int `bson:"port"`
		}
		_ = cur.All(ctx, &rows)
		for _, r := range rows {
			if r.Port > 0 {
				used[r.Port] = true
			}
		}
	}
	return used
}

// renderStartCmdFallback renders project-service start commands. The ${PORT}
// placeholder is expanded because we sometimes need the concrete value in the
// ExecStart line; systemd itself won't expand bash-style variables there.
// (Reuses the renderStartCmd helper from app_service.go — same substitution
// semantics — so projects and single-Apps handle `${PORT}` identically.)

// reconcileVhostFor regenerates the nginx vhost for a service and (re)issues a
// Let's Encrypt cert covering primary + all aliases. Called:
//   - on AddService (initial setup)
//   - on AddAlias / RemoveAlias (domain list changed)
//   - on UpdateService if PrimaryDomain is ever edited (not supported in v1)
//
// Multiple services in the same project can share a PrimaryDomain — typical
// pattern is a frontend on "/" and a backend on "/api". This function loads
// every sibling service with the same primary and merges their location
// blocks into a single vhost, so adding a backend doesn't silently clobber
// the frontend's nginx config.
//
// The cert lock is taken PER PRIMARY so two concurrent alias adds on the
// same service serialise; aliases on different primaries run in parallel.
//
// Callers MUST persist the target alias_domains to the DB before invoking
// this — buildMergedVhostSpec reads sibling rows (including the caller's
// own row when mid-mutation) and unions their alias_domains, so a stale
// DB row would leak the old aliases back into server_name.
func (s *ProjectService) reconcileVhostFor(ctx context.Context, proj *models.Project, role, primary string, aliases []string, pathPrefix string, port int, buildDir string) error {
	if primary == "" {
		return nil
	}

	lockRaw, _ := s.certLocks.LoadOrStore(primary, &sync.Mutex{})
	lock := lockRaw.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	if warnings := s.dnsPreflight(primary, aliases); len(warnings) > 0 {
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "[project %s] DNS: %s\n", proj.Slug, w)
		}
	}

	spec := s.buildMergedVhostSpec(ctx, proj.ID.Hex(), primary, aliases, role, pathPrefix, port, buildDir)

	hadCert := agent.LetsEncryptCertExists(primary)
	if hadCert {
		spec.UseSSL = true
	}
	if err := agent.CreateProjectVhost(ctx, spec); err != nil {
		return err
	}

	email := s.sslEmail
	if email == "" {
		email = "admin@" + primary
	}
	if err := agent.IssueLetsEncryptMulti(ctx, primary, spec.Aliases, email); err != nil {
		fmt.Fprintf(os.Stderr, "[project %s] certbot failed for %s: %v\n", proj.Slug, primary, err)
		return nil
	}

	spec.UseSSL = true
	return agent.CreateProjectVhost(ctx, spec)
}

// buildMergedVhostSpec combines every sibling service in the project that
// shares the same primary domain into one vhost spec. Aliases are union-ed
// across all siblings so each service can declare its own aliases without
// wiping the others'. The `newRole` / `newPort` / etc. parameters describe
// the service being added OR updated — if it already exists in the DB (same
// DB lookup finds it), DB values win; this lets the live DB stay the source
// of truth and prevents us from dropping a sibling's proxy block when an
// alias is added to another sibling.
func (s *ProjectService) buildMergedVhostSpec(ctx context.Context, projectIDHex, primary string, callerAliases []string, callerRole, callerPathPrefix string, callerPort int, callerBuildDir string) *agent.ProjectVhostSpec {
	projectID, _ := primitive.ObjectIDFromHex(projectIDHex)
	siblings, _ := s.listServicesForProject(ctx, projectID)

	spec := &agent.ProjectVhostSpec{
		PrimaryDomain: primary,
	}

	// Union of alias domains across every service that shares this primary,
	// plus the caller's aliases for services that haven't persisted yet.
	aliasSet := map[string]struct{}{}
	for _, a := range callerAliases {
		if a != "" && a != primary {
			aliasSet[a] = struct{}{}
		}
	}
	// Track whether any sibling already owns "/" so we know whether to add
	// the caller's "/" location or respect the DB state.
	callerHandled := false

	for _, sib := range siblings {
		if sib.PrimaryDomain != primary {
			continue
		}
		for _, a := range sib.AliasDomains {
			if a != "" && a != primary {
				aliasSet[a] = struct{}{}
			}
		}
		switch sib.Role {
		case "frontend", "static":
			if spec.Root == "" && sib.BuildDir != "" {
				spec.Root = sib.BuildDir
			}
		case "backend":
			pfx := sib.PathPrefix
			if pfx == "" {
				pfx = "/"
			}
			spec.Proxies = append(spec.Proxies, agent.ProjectProxyLoc{Prefix: pfx, Port: sib.Port})
		}
		// If this sibling happens to BE the caller (same role and primary),
		// we've now merged its state; don't re-add the caller's locations.
		if sib.Role == callerRole && sib.Port == callerPort && sib.PathPrefix == callerPathPrefix {
			callerHandled = true
		}
	}

	if !callerHandled {
		switch callerRole {
		case "frontend", "static":
			if spec.Root == "" && callerBuildDir != "" {
				spec.Root = callerBuildDir
			}
		case "backend":
			if callerPort > 0 {
				pfx := callerPathPrefix
				if pfx == "" {
					pfx = "/"
				}
				spec.Proxies = append(spec.Proxies, agent.ProjectProxyLoc{Prefix: pfx, Port: callerPort})
			}
		}
	}

	// Always treat `www.<primary>` and `cname.<primary>` as implicit
	// aliases, plus `www.<alias>` + `cname.<alias>` for every linked
	// alias. expandImplicitAliases bakes the same rule used by every
	// other caller (recoverProjectService, healers) so a service that
	// goes through the recovery / migration path lands with the same
	// nginx server_name + LE SAN list as one created via the normal
	// reconcile flow. Shared helper history: pre-3.1.11 missed www on
	// primary, pre-3.1.31 missed www on aliases, both caught live on
	// konsultkaro.com (Next.js apex) and integrator-linked shop
	// subdomains. See expandImplicitAliases for the recursion guard
	// against `www.www.X`.
	existing := make([]string, 0, len(aliasSet))
	for a := range aliasSet {
		existing = append(existing, a)
	}
	for _, a := range expandImplicitAliases(primary, existing) {
		aliasSet[a] = struct{}{}
	}

	aliases := make([]string, 0, len(aliasSet))
	for a := range aliasSet {
		aliases = append(aliases, a)
	}
	spec.Aliases = aliases
	return spec
}

// expandImplicitAliases returns the union of `aliases` with the implicit
// `www.<name>` + `cname.<name>` variant of `primary` and every entry in
// `aliases` that isn't already a www/cname variant. The result is the
// shape every nginx server_name AND every Let's Encrypt SAN list MUST
// carry so that:
//
//   - `https://<d>`, `https://www.<d>`, `https://cname.<d>` all serve
//     the same vhost with a cert that matches the URL bar, AND
//   - same holds for every operator-linked alias (`shop.example.com` →
//     `www.shop.example.com` + `cname.shop.example.com` covered too).
//
// Recursion guard: any input already prefixed with `www.` or `cname.`
// is left as-is — without this, calling the helper on already-expanded
// aliases would produce `www.www.<X>` and `cname.www.<X>` entries that
// nobody visits but that bloat the cert SAN list past Let's Encrypt's
// 100-name limit on large multi-domain projects.
//
// Exported package-private (lower-case) so the migration recovery path
// in transfer_panel_records.go can apply the SAME rule as the normal
// reconcileVhostFor flow — without this duplicate, transferred services
// landed with the raw alias_domains list and visitors hitting
// `https://www.<primary>` got a wrong-cert handshake after every
// migration (matching the user's "all details also same work with
// server migration time" requirement).
func expandImplicitAliases(primary string, aliases []string) []string {
	set := map[string]struct{}{}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		set[name] = struct{}{}
	}
	for _, a := range aliases {
		add(a)
	}
	if primary != "" && !strings.HasPrefix(primary, "www.") && !strings.HasPrefix(primary, "cname.") {
		add("www." + primary)
		add("cname." + primary)
	}
	// Snapshot before iterating so we don't expand newly-added entries.
	seeds := make([]string, 0, len(set))
	for a := range set {
		seeds = append(seeds, a)
	}
	for _, a := range seeds {
		if strings.HasPrefix(a, "www.") || strings.HasPrefix(a, "cname.") {
			continue
		}
		if a == primary {
			continue // already handled above
		}
		add("www." + a)
		add("cname." + a)
	}
	out := make([]string, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	return out
}



// aliasReconcileResult is the post-reconcile state surfaced back to the
// link/unlink-domain API consumer. Pre-3.1.31 the API returned the
// service object verbatim and a certbot failure was stderr-only, so an
// integrator who linked a domain whose DNS hadn't yet landed got 200
// success and zero indication that `https://<new-alias>` would serve
// the wrong cert. The result struct gives them a structured signal:
//
//   - SSLWarning is empty on success and human-readable on failure
//     ("certbot expand failed: <reason>", "alias missing from cert
//     SAN list", etc.) so the consumer can decide whether to retry,
//     surface a banner, or fail their own deploy.
//   - CoveredDomains is the parsed SAN list from the live cert AFTER
//     reconcile. Lets the consumer verify the cert actually covers
//     what they asked for without a second openssl round-trip.
type aliasReconcileResult struct {
	SSLWarning      string
	CoveredDomains  []string
}

// reconcileVhostForAliasChange is the alias-link / alias-unlink wrapper
// around reconcileVhostFor. Runs the standard reconcile (vhost + cert
// expansion) AND inspects the resulting cert so the API caller learns
// whether the SSL leg actually succeeded for the freshly-touched domain.
//
// `targetDomain` is the domain whose SSL coverage matters most to the
// caller — for AddAlias that's the new alias, for RemoveAlias it's the
// primary (so the caller can confirm the cert still covers the kept
// aliases). Empty `targetDomain` skips the SAN-coverage check and just
// surfaces whatever certbot said.
func (s *ProjectService) reconcileVhostForAliasChange(ctx context.Context, proj *models.Project, svc *models.ProjectService, aliases []string, targetDomain string) (*aliasReconcileResult, error) {
	res := &aliasReconcileResult{}

	if err := s.reconcileVhostFor(ctx, proj, svc.Role, svc.PrimaryDomain, aliases, svc.PathPrefix, svc.Port, svc.BuildDir); err != nil {
		// Hard nginx / vhost-write failure — surface as error so the
		// API returns 5xx and the caller knows the link DID NOT
		// take effect at all (alias was already persisted to Mongo
		// in step 1, but nginx never reloaded with it).
		return nil, err
	}

	covered := agent.LetsEncryptCertSANs(svc.PrimaryDomain)
	res.CoveredDomains = covered
	if len(covered) == 0 {
		// No live cert at all yet (or openssl couldn't read it). The
		// vhost is up on HTTP-only and `bzpanel mail-ssl-sweep` /
		// next reconcile will pick the cert up once DNS is healthy.
		res.SSLWarning = fmt.Sprintf("nginx vhost reconciled without SSL — no Let's Encrypt cert found for %s yet (DNS for the alias may not be pointing at this server, or the HTTP-01 challenge failed). Re-run from the SSL page once DNS is in place.", svc.PrimaryDomain)
		return res, nil
	}
	if targetDomain != "" {
		if !sanCovers(covered, targetDomain) {
			res.SSLWarning = fmt.Sprintf("alias %s linked + nginx vhost updated, but the Let's Encrypt cert for %s does NOT yet cover %s (last certbot run failed). Most common cause: DNS for %s is not yet resolving to this server's IP. Once DNS is in place, hit Reissue on the SSL page.", targetDomain, svc.PrimaryDomain, targetDomain, targetDomain)
		}
	}
	return res, nil
}

// sanCovers checks whether `host` is covered by any entry in `sans`,
// honouring `*.<parent>` wildcards (a single level only — matching
// certbot / Let's Encrypt's RFC-6125 §6.4.3 behaviour). Case folded
// so `Host.Example.COM` matches a `host.example.com` SAN.
func sanCovers(sans []string, host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, raw := range sans {
		san := strings.ToLower(strings.TrimSpace(raw))
		if san == "" {
			continue
		}
		if san == host {
			return true
		}
		if strings.HasPrefix(san, "*.") {
			suffix := san[1:] // ".example.com"
			if strings.HasSuffix(host, suffix) && strings.Count(host, ".") == strings.Count(suffix, ".") {
				return true
			}
		}
	}
	return false
}

// dnsPreflight is the soft sanity check. Returns human-readable warning
// strings; never errors. Checks:
//   - PrimaryDomain's A records include the configured ServerIP
//   - each alias CNAMEs to PrimaryDomain (trailing dot trimmed)
func (s *ProjectService) dnsPreflight(primary string, aliases []string) []string {
	var warnings []string
	if primary == "" {
		return warnings
	}
	res := &net.Resolver{PreferGo: true}
	ctx, cancel := contextWithTimeout(8 * time.Second)
	defer cancel()

	if s.serverIP != "" {
		ips, err := res.LookupIP(ctx, "ip4", primary)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: A record not yet published (%v)", primary, err))
		} else {
			hit := false
			for _, ip := range ips {
				if ip.String() == s.serverIP {
					hit = true
					break
				}
			}
			if !hit {
				warnings = append(warnings, fmt.Sprintf("%s: A record does not point at %s", primary, s.serverIP))
			}
		}
	}
	for _, a := range aliases {
		if a == "" {
			continue
		}
		cname, err := res.LookupCNAME(ctx, a)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: CNAME lookup failed (%v)", a, err))
			continue
		}
		if strings.TrimSuffix(cname, ".") != primary {
			warnings = append(warnings, fmt.Sprintf("%s: CNAME target is %q, expected %q", a, strings.TrimSuffix(cname, "."), primary))
		}
	}
	return warnings
}

// contextWithTimeout is a tiny wrapper so project_helpers.go can build a
// context without importing context+time in every caller.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// appendLog is the minimal fire-and-forget log writer used by the deploy
// worker. Best-effort — a missing /var/log dir shouldn't fail a deploy.
func appendLog(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	f.WriteString(line)
}
