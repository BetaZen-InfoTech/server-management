package services

import (
	"context"
	"fmt"
	"net"
	"os"
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

// validateServiceName keeps names safe for systemd unit files, shell
// interpolation, and MongoDB unique-index compound keys.
func validateServiceName(name string) error {
	if !serviceNamePattern.MatchString(name) {
		return fmt.Errorf("invalid service name %q: must be 2-32 chars, lowercase, start with a letter, only a-z 0-9 and '-'", name)
	}
	return nil
}

// defaultProjectUser builds the Linux username for a project when the caller
// hasn't picked one. Prefixed with 'sp-' so it's obvious in /etc/passwd that
// the panel created the account, and kept short enough (32 chars) to avoid
// useradd's NAME_REGEX limit on Debian.
func defaultProjectUser(slug string) string {
	name := "sp-" + slug
	if len(name) > 30 {
		name = name[:30]
	}
	return name
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
