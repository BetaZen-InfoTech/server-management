package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// waitForPort blocks until a TCP connect to 127.0.0.1:port succeeds, up to
// the given timeout. Used after starting a systemd service so the reverse
// proxy sees a live upstream on first request instead of racing into 502.
func waitForPort(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

// probeHealthEndpoint hits the configured health-check path on 127.0.0.1
// and returns (statusCode, ok). ok is true when the response code is in
// the 2xx / 3xx range — that's treated as "app is up and answering", same
// as a basic uptime monitor. Used after waitForPort so a 502 from the
// app's own code (crashed listener, bad import, missing env var) shows
// up as a deploy warning instead of a silent reverse-proxy 502 later.
//
// path should start with "/"; an empty path defaults to "/".
func probeHealthEndpoint(port int, path string, timeout time.Duration) (int, bool) {
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	client := &http.Client{Timeout: timeout}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	resp, err := client.Get(url)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.StatusCode >= 200 && resp.StatusCode < 400
}

var appNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// validateAppName ensures the app name is safe for use in systemd unit names,
// shell arguments, and filesystem paths. Lowercase alphanumeric with dashes,
// must start with a letter, 2-32 chars.
func validateAppName(name string) error {
	if !appNamePattern.MatchString(name) {
		return fmt.Errorf("invalid app name %q: must be 2-32 chars, lowercase, start with a letter, only a-z 0-9 and '-'", name)
	}
	return nil
}

// normalizeAppType canonicalises type synonyms sent from different frontends.
func normalizeAppType(t string) string {
	switch strings.ToLower(t) {
	case "nodejs", "node":
		return "node"
	case "nextjs":
		return "node"
	default:
		return strings.ToLower(t)
	}
}

// sanitizeDomain trims and lowercases a domain for nginx consistency.
func sanitizeDomain(d string) string {
	return strings.ToLower(strings.TrimSpace(d))
}

// ensureUser creates a system user if it does not already exist. The home
// directory is created at /home/<user> and a dedicated group is created with
// the same name. No-op if the user exists.
func ensureUser(ctx context.Context, user string) error {
	if user == "" {
		return fmt.Errorf("system user is required")
	}
	// Check if user exists via `id`
	if _, err := agent.RunCommand(ctx, "id", user); err == nil {
		return nil
	}
	if _, err := agent.RunCommand(ctx, "useradd", "-m", "-s", "/bin/bash", user); err != nil {
		return fmt.Errorf("failed to create user %s: %w", user, err)
	}
	return nil
}

// allocatePort picks a free TCP port in the 3100-3999 range that is not
// currently bound and not already used by another deployed app in MongoDB.
// Falls back to any free port if the preferred range is exhausted.
func allocatePort(usedPorts map[int]bool) (int, error) {
	for port := 3100; port < 4000; port++ {
		if usedPorts[port] {
			continue
		}
		if isPortFree(port) {
			return port, nil
		}
	}
	// Fallback: kernel-assigned
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func isPortFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// writeFileAsUser writes a file with contents owned by the given user. It
// uses shell redirection so the ServerPanel process (running as root) can
// create files that the app user can subsequently read or execute.
func writeFileAsUser(ctx context.Context, path, contents, user string, mode string) error {
	// Ensure parent dir exists with correct ownership.
	dir := filepath.Dir(path)
	if _, err := agent.RunCommand(ctx, "install", "-d", "-o", user, "-g", user, "-m", "0755", dir); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	// Write via heredoc to avoid quoting issues with arbitrary content.
	marker := "SP_SCAFFOLD_EOF"
	script := fmt.Sprintf("cat > %q <<'%s'\n%s\n%s\n", path, marker, contents, marker)
	if _, err := agent.RunCommand(ctx, "bash", "-c", script); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := agent.RunCommand(ctx, "chown", user+":"+user, path); err != nil {
		return err
	}
	if mode != "" {
		if _, err := agent.RunCommand(ctx, "chmod", mode, path); err != nil {
			return err
		}
	}
	return nil
}

// prepareAppDir removes any prior deployment at appDir and recreates it with
// the correct ownership, so a fresh git clone or scaffold write can succeed.
func prepareAppDir(ctx context.Context, appDir, user string) error {
	if _, err := agent.RunCommand(ctx, "rm", "-rf", appDir); err != nil {
		return fmt.Errorf("cleanup %s: %w", appDir, err)
	}
	if _, err := agent.RunCommand(ctx, "install", "-d", "-o", user, "-g", user, "-m", "0755", appDir); err != nil {
		return fmt.Errorf("mkdir %s: %w", appDir, err)
	}
	// Also ensure /home/<user>/apps exists with correct ownership.
	parent := filepath.Dir(appDir)
	agent.RunCommand(ctx, "install", "-d", "-o", user, "-g", user, "-m", "0755", parent)
	return nil
}

// chownRecursive sets ownership of appDir and everything inside it to user:user.
func chownRecursive(ctx context.Context, appDir, user string) error {
	_, err := agent.RunCommand(ctx, "chown", "-R", user+":"+user, appDir)
	return err
}

// secureRandomHex returns a hex string of n bytes (so 2*n chars). Used to
// mint webhook IDs and HMAC secrets so each app's auto-deploy URL/secret
// is unguessable. Falls back to a deterministic timestamp-derived value
// only if the OS RNG fails — which on Linux means something is very
// wrong, but we'd rather generate *something* than fail the deploy.
//
// (Distinct name from wordpress_service.randomHex to avoid the package-
// scope redeclaration; behavior is identical except for the fallback.)
func secureRandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%016x%016x", time.Now().UnixNano(), time.Now().Unix())[:2*n]
	}
	return hex.EncodeToString(b)
}

// validateRepoSubpath rejects subpaths that would let a deploy escape its
// install directory. Allows things like "apps/admin", "packages/api/src";
// rejects "..", absolute paths, and traversal sequences.
func validateRepoSubpath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", nil
	}
	// Strip leading/trailing slashes — the caller treats subpath as a
	// relative segment, so "/apps/admin/" and "apps/admin" should behave
	// identically instead of producing absolute joins.
	p = strings.Trim(p, "/")
	if p == "" {
		return "", nil
	}
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("repo_subpath must be relative (e.g. apps/admin), not absolute")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == "." {
			return "", fmt.Errorf("repo_subpath must not contain '.' or '..' segments")
		}
		if seg == "" {
			return "", fmt.Errorf("repo_subpath must not contain empty segments")
		}
	}
	return p, nil
}

// resolveRuntimeBinDir maps a user-specified (app_type, runtime_version) to
// the directory containing that version's interpreter/compiler binaries.
// Returns "" when no override applies — the caller falls back to PATH
// defaults (/usr/local/bin, /usr/local/go/bin, etc.).
//
// Supported layouts (match what agent/software.go installs):
//
//	node  → /usr/local/n/versions/node/<version>/bin
//	go    → /opt/go/<version>/bin
//	ruby  → /opt/ruby/<version>/bin
//	python → /usr/bin (system; versioned pythons use venv instead)
//
// The caller is expected to prepend this to PATH so `node`/`go`/`ruby`
// resolve to the pinned version during both build and systemd ExecStart.
func resolveRuntimeBinDir(appType, version string) string {
	v := strings.TrimSpace(version)
	if v == "" {
		return ""
	}
	// Accept forms like "node-20", "node 20", "20.11.0"; strip non-numeric
	// runtime-name prefixes so just the version portion is left.
	for _, prefix := range []string{"node-", "nodejs-", "go-", "golang-", "ruby-", "python-"} {
		if strings.HasPrefix(strings.ToLower(v), prefix) {
			v = v[len(prefix):]
			break
		}
	}
	switch strings.ToLower(appType) {
	case "node", "nodejs":
		return fmt.Sprintf("/usr/local/n/versions/node/%s/bin", v)
	case "go", "golang":
		return fmt.Sprintf("/opt/go/%s/bin", v)
	case "ruby":
		return fmt.Sprintf("/opt/ruby/%s/bin", v)
	}
	return ""
}

// runBuildAsUser runs the build command inside appDir as the app user. Uses
// `sudo -u` + login-like shell so NVM/rbenv/pyenv etc. still work, and
// prepends the common binary locations to PATH so node/npm (installed at
// /usr/local/bin) and go (installed at /usr/local/go/bin) are found
// without the operator having to know their absolute paths.
//
// When runtimeBinDir is non-empty (set by the caller from the app's
// runtime_version field), it goes to the front of PATH so the pinned
// interpreter wins over the system default — pick Node 20 vs Node 22,
// Go 1.22 vs 1.23, etc.
func runBuildAsUser(ctx context.Context, user, appDir, buildCmd, runtimeBinDir string) error {
	pathPrefix := ""
	if runtimeBinDir != "" {
		pathPrefix = runtimeBinDir + ":"
	}
	// NPM_CONFIG_PRODUCTION=false forces `npm install` to include devDeps even
	// when something in the user's profile sets NODE_ENV=production. Without
	// this, projects whose start_cmd references a devDependency (e.g.
	// cross-env, ts-node, tsx) crash-loop at runtime with "command not found"
	// because the package never made it to node_modules. Explicit production
	// installs (e.g. `npm install --production` in install_cmd) still win
	// because npm CLI flags override config env vars.
	script := fmt.Sprintf("export PATH=%s/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin:$PATH; export HOME=/home/%s; export GOCACHE=/home/%s/.cache/go-build; export GOMODCACHE=/home/%s/go/pkg/mod; export NPM_CONFIG_PRODUCTION=false; cd %q && %s", pathPrefix, user, user, user, appDir, buildCmd)
	res, err := agent.RunCommand(ctx, "sudo", "-u", user, "-H", "bash", "-lc", script)
	if err != nil {
		tail := res.Error
		if len(tail) > 800 {
			tail = tail[len(tail)-800:]
		}
		return fmt.Errorf("build failed: %s", tail)
	}
	return nil
}

// runInstallAsUser runs an ad-hoc install command (npm install, pip install,
// bundle add, etc.) inside appDir as the app user, and returns the merged
// stdout+stderr along with a boolean success flag. The full output is the
// whole point — the caller streams it back to the operator so they can see
// why a package install succeeded or failed.
//
// runtimeBinDir may be empty; when set, it pins PATH to a specific
// runtime version so `npm install` under app X uses Node 20 while app Y
// uses Node 22 without them fighting over /usr/local/bin.
func runInstallAsUser(ctx context.Context, user, appDir, cmd, runtimeBinDir string) (string, bool) {
	pathPrefix := ""
	if runtimeBinDir != "" {
		pathPrefix = runtimeBinDir + ":"
	}
	script := fmt.Sprintf(
		"export PATH=%s/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin:$PATH; export HOME=/home/%s; export GOCACHE=/home/%s/.cache/go-build; export GOMODCACHE=/home/%s/go/pkg/mod; export NPM_CONFIG_PRODUCTION=false; cd %q && %s 2>&1",
		pathPrefix, user, user, user, appDir, cmd)
	res, err := agent.RunCommand(ctx, "sudo", "-u", user, "-H", "bash", "-lc", script)
	var out string
	if res != nil {
		out = res.Output
		if out == "" && res.Error != "" {
			out = res.Error
		}
	}
	return out, err == nil
}

// buildPM2Ecosystem returns the contents of an ecosystem.config.js file that
// runs an arbitrary shell command under PM2. PM2 handles crash recovery,
// restart throttling, and memory limits; systemd keeps pm2-runtime itself
// alive. Using interpreter:"none" with bash -lc lets us wrap any start
// command (node, npx, gunicorn, etc.) without parsing it.
//
// cwd is set explicitly so PM2 doesn't fall back to whatever directory the
// caller's shell happens to be in — without it, `bash -lc "node server.js"`
// can resolve server.js against the wrong dir when bash login files cd
// elsewhere.
func buildPM2Ecosystem(name, startCmd, cwd string, port int, envVars map[string]string, minInstances, maxInstances int) string {
	jsEscape := func(s string) string {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		return s
	}
	var envLines []string
	if port > 0 {
		envLines = append(envLines, fmt.Sprintf(`      "PORT": "%d"`, port))
	}
	for k, v := range envVars {
		envLines = append(envLines, fmt.Sprintf(`      "%s": "%s"`, jsEscape(k), jsEscape(v)))
	}
	envBlock := strings.Join(envLines, ",\n")
	cwdLine := ""
	if cwd != "" {
		cwdLine = fmt.Sprintf(`    cwd: "%s",`+"\n", jsEscape(cwd))
	}
	// Wrap the start command in `cd <cwd> && ...` as well so server.js and
	// other relative paths resolve correctly even if PM2 ignores the cwd
	// option for some reason.
	wrappedCmd := startCmd
	if cwd != "" {
		wrappedCmd = fmt.Sprintf("cd %s && %s", cwd, startCmd)
	}

	// Instance count + exec_mode. Single-instance apps keep the simple
	// fork model (PM2's default) — switching to cluster mode for N=1
	// would give us nothing and would break apps that bind to a TCP
	// port non-shareably. When the operator asks for >1 instances we
	// switch to cluster mode so PM2 round-robins traffic across N
	// workers behind a single listening socket.
	instancesLine := ""
	execModeLine := ""
	instances := minInstances
	if maxInstances > instances {
		instances = maxInstances
	}
	if instances > 1 {
		instancesLine = fmt.Sprintf(`    instances: %d,`+"\n", instances)
		execModeLine = `    exec_mode: "cluster",` + "\n"
	}

	return fmt.Sprintf(`module.exports = {
  apps: [{
    name: "%s",
    script: "bash",
    args: ["-lc", "%s"],
    interpreter: "none",
%s%s%s    autorestart: true,
    max_restarts: 10,
    restart_delay: 2000,
    max_memory_restart: "512M",
    env: {
%s
    }
  }]
};
`, jsEscape(name), jsEscape(wrappedCmd), cwdLine, instancesLine, execModeLine, envBlock)
}

// detectCustomNodeStart inspects a freshly-deployed node app dir for a
// custom-server setup and returns a start command that runs it directly.
// Returns empty string when the project looks like a vanilla Next.js /
// Express app that the caller's own start_cmd can handle.
//
// Why: the `nextjs` preset defaults start_cmd to `npx next start -p ${PORT}`,
// which only works against a standard next-build output. Many real-world
// Next apps ship a custom-server.js (Express + next as a request handler)
// and fail with "Cannot read properties of undefined" or similar when
// started via `next start`. We look for a server.js / server.mjs / index.js
// that package.json's "start" script points at, and prefer running it via
// `node <file>` with NODE_ENV=production so the user's code path matches
// what `npm start` would do.
func detectCustomNodeStart(ctx context.Context, appDir string) string {
	// Inspect package.json.start — if it exists and points at a local .js
	// entry, that's authoritative. We don't try to execute `npm start`
	// because it would depend on which package manager is installed.
	res, err := agent.RunCommand(ctx, "cat", filepath.Join(appDir, "package.json"))
	if err != nil || res == nil || res.Output == "" {
		return ""
	}
	// Cheap JSON probe — the full parse is more reliable but the pattern
	// catches the common "start": "... node <file>..." shape in one pass.
	re := regexp.MustCompile(`"start"\s*:\s*"([^"]+)"`)
	m := re.FindStringSubmatch(res.Output)
	if len(m) < 2 {
		return ""
	}
	startScript := m[1]

	// Extract the js entry file from the start script. Patterns handled:
	//   "cross-env NODE_ENV=production node server.js"
	//   "node server.mjs"
	//   "NODE_ENV=production node ./custom/server.js"
	entryRe := regexp.MustCompile(`\bnode\s+(\S+\.(?:m?js|cjs))`)
	em := entryRe.FindStringSubmatch(startScript)
	if len(em) < 2 {
		return ""
	}
	entry := strings.TrimPrefix(em[1], "./")

	// Only trust the entry when the file actually exists in appDir — the
	// start script might reference a file that lives in node_modules or is
	// generated by build, which we don't want to launch directly.
	if _, err := agent.RunCommand(ctx, "test", "-f", filepath.Join(appDir, entry)); err != nil {
		return ""
	}

	// Use bare `node` so runtime_version overrides via PATH still apply —
	// the systemd unit's Environment=PATH= puts the pinned node first.
	return fmt.Sprintf("NODE_ENV=production node %s", entry)
}

// buildStaticVhostConfig returns the nginx server block contents for a
// static site served directly from disk with a SPA fallback.
func buildStaticVhostConfig(domain, root string) string {
	return fmt.Sprintf(`server {
    listen 80;
    server_name %s;
    root %s;
    index index.html;

    access_log /var/log/nginx/%s-access.log;
    error_log /var/log/nginx/%s-error.log;

    location / {
        try_files $uri $uri/ /index.html;
    }
}
`, domain, root, domain, domain)
}

// ensureSSLForApp issues a Let's Encrypt cert for the app's domain (with
// retries for DNS propagation) and recreates the vhost with the SSL variant
// so port 443 starts answering immediately. Best-effort: failures are logged
// to stderr and don't break the deploy — the operator can re-issue later
// from the SSL page once DNS resolves to this server.
func ensureSSLForApp(ctx context.Context, db *mongo.Database, domain string, isStatic bool, port int, servedDir string) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return
	}

	sslEmail := strings.TrimSpace(os.Getenv("SSL_EMAIL"))
	if sslEmail == "" {
		sslEmail = "admin@" + domain
	}

	// Skip the issuance round-trip if a cert already exists (re-deploy of an
	// app whose domain is already SSL'd). We still recreate the vhost with
	// the SSL variant below so the port-443 server block points at the
	// (possibly new) port / static dir.
	if !agent.LetsEncryptCertExists(domain) {
		additional := []string{"www." + domain}
		var lastErr error
		for attempt := 1; attempt <= 3; attempt++ {
			lastErr = agent.IssueLetsEncrypt(ctx, domain, sslEmail, additional, false)
			if lastErr == nil {
				break
			}
			fmt.Fprintf(os.Stderr, "warning: app auto-SSL attempt %d failed for %s: %v\n", attempt, domain, lastErr)
			time.Sleep(3 * time.Second)
		}
		if lastErr != nil {
			fmt.Fprintf(os.Stderr, "warning: app auto-SSL failed after 3 attempts for %s: %v\n", domain, lastErr)
			return
		}
	}

	// Cert is on disk — swap the HTTP-only vhost for the SSL variant.
	if isStatic {
		if err := agent.CreateStaticVhostWithSSL(ctx, domain, servedDir, "", ""); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to upgrade static vhost to SSL for %s: %v\n", domain, err)
			return
		}
	} else if port > 0 {
		if err := agent.CreateReverseProxyWithSSL(ctx, &agent.VhostConfig{Domain: domain, Port: port}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to upgrade reverse-proxy vhost to SSL for %s: %v\n", domain, err)
			return
		}
	}

	// Persist a cert record + flip the domain's ssl_active flag if a domain
	// record exists. Best-effort — the cert is already live regardless.
	now := time.Now()
	cert := models.SSLCertificate{
		Domain:    domain,
		Type:      "letsencrypt",
		Domains:   []string{domain, "www." + domain},
		AutoRenew: true,
		KeyType:   "RSA",
		CertPath:  fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", domain),
		KeyPath:   fmt.Sprintf("/etc/letsencrypt/live/%s/privkey.pem", domain),
		CreatedAt: now,
		UpdatedAt: now,
	}
	// Avoid duplicate cert rows on re-deploy.
	sslCol := db.Collection(database.ColSSLCerts)
	if n, _ := sslCol.CountDocuments(ctx, bson.M{"domain": domain}); n == 0 {
		sslCol.InsertOne(ctx, cert)
	}
	db.Collection(database.ColDomains).UpdateOne(ctx, bson.M{"domain": domain}, bson.M{
		"$set": bson.M{"ssl_active": true, "updated_at": now},
	})
}

// restoreDomainBaseVhost rebuilds the original PHP-FPM + public_html vhost
// for a domain after an app/project/service that "owned" it is removed.
// This is the user-visible "go back to first state" behaviour: deleting a
// deployed app on example.com returns it to serving /home/<user>/domains/
// example.com/public_html via PHP-FPM (the same shape DomainService.Create
// originally set up), instead of the generic "Site not deployed" placeholder.
//
// SSL is preserved when the domain has a Let's Encrypt cert on disk OR the
// Domain.SSLActive flag is set — we re-bind it to the restored vhost via
// CreateVhostWithSSL so HTTPS keeps working without re-issuing.
//
// Falls back to the placeholder when:
//   - the domain isn't in the Domains collection (orphan / never-registered),
//   - the domain's user account or document root doesn't exist on disk.
//
// Best-effort: a placeholder is always written if restoration fails, so the
// domain never falls through to nginx's alphabetical-first 443 server (which
// would serve the wrong cert and break SNI for unrelated sites).
func restoreDomainBaseVhost(ctx context.Context, db *mongo.Database, domainName string) {
	domainName = strings.TrimSpace(strings.ToLower(domainName))
	if domainName == "" {
		return
	}
	var dom models.Domain
	err := db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": domainName}).Decode(&dom)
	if err != nil {
		// No registered domain — placeholder is the safest fallback.
		_ = agent.WritePlaceholderVhost(ctx, domainName)
		return
	}
	// Verify the doc-root exists; if not, the user dir was wiped (or the
	// domain was registered without ever creating a public_html). Recreate
	// it so the restored vhost has somewhere to serve from.
	docRoot := fmt.Sprintf("/home/%s/domains/%s/public_html", dom.User, dom.Domain)
	if _, err := agent.RunCommand(ctx, "test", "-d", docRoot); err != nil {
		if cdErr := agent.CreateDomainDirectory(ctx, dom.User, dom.Domain); cdErr != nil {
			fmt.Fprintf(os.Stderr, "warning: doc-root recreate failed for %s: %v\n", dom.Domain, cdErr)
			_ = agent.WritePlaceholderVhost(ctx, domainName)
			return
		}
	}
	// Ensure the PHP-FPM pool exists (it should — DomainService.Create made
	// it — but a previous app deploy may have removed it).
	_ = agent.CreatePHPPool(ctx, dom.Domain, dom.User, dom.PHPVersion)

	cfg := &agent.VhostConfig{
		Domain:     dom.Domain,
		User:       dom.User,
		PHPVersion: dom.PHPVersion,
	}
	hasCert := agent.LetsEncryptCertExists(dom.Domain)
	var restoreErr error
	if hasCert || dom.SSLActive {
		restoreErr = agent.CreateVhostWithSSL(ctx, cfg)
	} else {
		restoreErr = agent.CreateVhost(ctx, cfg)
	}
	if restoreErr != nil {
		fmt.Fprintf(os.Stderr, "warning: restore vhost failed for %s: %v — falling back to placeholder\n", dom.Domain, restoreErr)
		_ = agent.WritePlaceholderVhost(ctx, domainName)
		return
	}
	// Sync ssl_active in DB if a cert is on disk but the flag was stale.
	if hasCert && !dom.SSLActive {
		db.Collection(database.ColDomains).UpdateOne(ctx, bson.M{"_id": dom.ID}, bson.M{
			"$set": bson.M{"ssl_active": true, "updated_at": time.Now()},
		})
	}
}
