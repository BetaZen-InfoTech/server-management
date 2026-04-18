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

// detectListeningPort returns the first TCP port the given systemd unit's
// MainPID is listening on, by cross-referencing `ss -ltnp`. Polls until a
// listener appears or `timeout` elapses. Returns 0 if nothing is found —
// the caller keeps whatever port it originally allocated.
//
// Motivation: apps that hardcode a listen port (e.g. a server.js with
// `app.listen(4096)` that ignores process.env.PORT) otherwise end up behind
// a reverse-proxy vhost pointing at a port nothing is listening on, which
// serves every request as 502 Bad Gateway. This reconciles the two.
func detectListeningPort(ctx context.Context, unitName string, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pidRes, err := agent.RunCommand(ctx, "systemctl", "show", "-p", "MainPID", "--value", unitName)
		if err == nil && pidRes != nil {
			pid := strings.TrimSpace(pidRes.Output)
			if pid != "" && pid != "0" {
				// Child processes (node, python, etc.) usually bind — not
				// the shell that systemd exec'd. Search every PID whose
				// session leader is MainPID via /proc/<pid>/stat or fall
				// back to matching any pid=<MainPID> entry in ss output.
				ssRes, err := agent.RunCommand(ctx, "bash", "-c",
					fmt.Sprintf(`ss -ltnp 2>/dev/null | awk -v pid=%s '$0 ~ ("pid=" pid ",") {print $4}'`, pid))
				if err == nil && ssRes != nil {
					for _, line := range strings.Split(ssRes.Output, "\n") {
						line = strings.TrimSpace(line)
						if line == "" {
							continue
						}
						if idx := strings.LastIndex(line, ":"); idx >= 0 {
							if p, err := strconv.Atoi(line[idx+1:]); err == nil && p > 0 {
								return p
							}
						}
					}
				}
				// Fallback: walk /proc/<PID>/task/*/children to find any
				// descendant, then repeat the ss lookup. Needed for apps
				// launched under a shell/wrapper that doesn't itself bind.
				childRes, _ := agent.RunCommand(ctx, "bash", "-c",
					fmt.Sprintf(`pgrep -P %s 2>/dev/null | head -5 | tr '\n' ' '`, pid))
				if childRes != nil {
					for _, child := range strings.Fields(childRes.Output) {
						ssRes2, err := agent.RunCommand(ctx, "bash", "-c",
							fmt.Sprintf(`ss -ltnp 2>/dev/null | awk -v pid=%s '$0 ~ ("pid=" pid ",") {print $4}'`, child))
						if err != nil || ssRes2 == nil {
							continue
						}
						for _, line := range strings.Split(ssRes2.Output, "\n") {
							line = strings.TrimSpace(line)
							if line == "" {
								continue
							}
							if idx := strings.LastIndex(line, ":"); idx >= 0 {
								if p, err := strconv.Atoi(line[idx+1:]); err == nil && p > 0 {
									return p
								}
							}
						}
					}
				}
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
	return 0
}

var serviceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
var envVarKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var branchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)

// validateServiceName keeps names safe for systemd unit files, shell
// interpolation, and MongoDB unique-index compound keys.
func validateServiceName(name string) error {
	if !serviceNamePattern.MatchString(name) {
		return fmt.Errorf("invalid service name %q: must be 2-32 chars, lowercase, start with a letter, only a-z 0-9 and '-'", name)
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
// The `skipServiceID` parameter is used when removing a service: pass the
// ID being removed so it's excluded from the regenerated merged vhost.
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

	aliases := make([]string, 0, len(aliasSet))
	for a := range aliasSet {
		aliases = append(aliases, a)
	}
	spec.Aliases = aliases
	return spec
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
