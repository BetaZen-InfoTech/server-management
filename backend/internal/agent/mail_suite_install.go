package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// goBootstrapVersion is the Go toolchain version downloaded if `go`
// isn't already on the box. Pinned so installs are reproducible; bump
// in lockstep with mail-suite/backend/go.mod's minimum.
const goBootstrapVersion = "1.22.5"

// MailSuiteInstallOptions controls how InstallMailSuite provisions the
// mail-suite product on this server.
type MailSuiteInstallOptions struct {
	Domain        string // e.g. mail.example.com — used for the nginx vhost + Let's Encrypt cert
	MongoURI      string // Mongo connection string for the mail_suite DB
	MongoDBName   string // defaults to "mail_suite"
	PanelURL      string // Betazen panel URL the mail-suite backend will call for DNS
	PanelToken    string // token the mail-suite backend uses to authenticate to the panel
	JWTSecret     string // optional — generated if empty
	ServiceToken  string // optional — generated if empty (token the panel uses to call mail-suite)
	BinarySource  string // optional path to a prebuilt mail-suite binary; defaults to <repo>/mail-suite/backend (built in place)
	InstallDir    string // defaults to /opt/mail-suite
}

// MailSuiteInstallResult is what the caller registers in the panel.
type MailSuiteInstallResult struct {
	APIURL        string
	WebmailURL    string
	ServiceToken  string
	InstallDir    string
	BinaryPath    string
	SystemdUnit   string
	NginxVhost    string
}

// InstallMailSuite runs an idempotent provisioning sequence:
//
//   1. Create install dir.
//   2. Build (or copy) the mail-suite binary.
//   3. Write .env (auto-generating JWT_SECRET / SERVICE_TOKEN if blank).
//   4. Write the systemd unit and enable+start it.
//   5. Write an nginx vhost (mail-suite is reverse-proxied at /).
//   6. Provision a Let's Encrypt cert with certbot --nginx (best effort).
//
// Returns the public API URL + the service token the panel should store.
func InstallMailSuite(ctx context.Context, opts MailSuiteInstallOptions) (*MailSuiteInstallResult, error) {
	if strings.TrimSpace(opts.Domain) == "" {
		return nil, fmt.Errorf("domain required")
	}
	if opts.InstallDir == "" {
		opts.InstallDir = "/opt/mail-suite"
	}
	if opts.MongoDBName == "" {
		opts.MongoDBName = "mail_suite"
	}
	if opts.MongoURI == "" {
		opts.MongoURI = "mongodb://localhost:27017"
	}
	if opts.JWTSecret == "" {
		s, err := randomHex(32)
		if err != nil {
			return nil, fmt.Errorf("jwt secret: %w", err)
		}
		opts.JWTSecret = s
	}
	if opts.ServiceToken == "" {
		s, err := randomHex(32)
		if err != nil {
			return nil, fmt.Errorf("service token: %w", err)
		}
		opts.ServiceToken = s
	}

	binaryPath := filepath.Join(opts.InstallDir, "mail-suite")
	systemdUnit := "/etc/systemd/system/mail-suite.service"
	nginxVhost := "/etc/nginx/sites-available/mail-suite.conf"
	nginxLink := "/etc/nginx/sites-enabled/mail-suite.conf"
	envPath := filepath.Join(opts.InstallDir, ".env")

	// 1. dirs
	if err := os.MkdirAll(opts.InstallDir, 0750); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", opts.InstallDir, err)
	}

	// 2a. backend binary
	backendSrc, webmailSrc := findMailSuiteSources()
	if opts.BinarySource != "" {
		if _, err := RunCommand(ctx, "install", "-m", "0755", opts.BinarySource, binaryPath); err != nil {
			return nil, fmt.Errorf("install binary: %w", err)
		}
	} else {
		if backendSrc == "" {
			return nil, fmt.Errorf("mail-suite backend source not found in /opt/server-panel/mail-suite/backend or $PWD/mail-suite/backend")
		}
		goBin, err := ensureGo(ctx)
		if err != nil {
			return nil, fmt.Errorf("ensure go: %w", err)
		}
		// `go build` writes to opts.InstallDir/mail-suite directly.
		// GOOS/GOARCH are belt-and-braces — the server is the build
		// target, but pinning prevents a misconfigured GOOS env from
		// emitting a stray Darwin binary.
		if _, err := RunCommandLong(ctx, 10*time.Minute, "bash", "-lc",
			fmt.Sprintf("cd %s && GOOS=linux GOARCH=amd64 %s build -o %s ./cmd/server",
				shellQuote(backendSrc), shellQuote(goBin), shellQuote(binaryPath))); err != nil {
			return nil, fmt.Errorf("go build: %w", err)
		}
	}

	// 2b. webmail SPA — build with npm and copy dist/ into the install
	// dir. Without this step `GET https://<domain>/` returns 404
	// "Cannot GET /" from Fiber. Best-effort: a failure here leaves
	// the backend running with the API live; main.go will log and
	// skip the static mount.
	webmailDir := filepath.Join(opts.InstallDir, "webmail")
	if webmailSrc != "" {
		distDir := filepath.Join(webmailSrc, "dist")
		// Reuse a previous build if it's already there; the webmail
		// source is rarely edited on the server, so re-running this
		// step shouldn't repeat the multi-minute npm install.
		if _, err := os.Stat(filepath.Join(distDir, "index.html")); err != nil {
			npm, err := ensureNode(ctx)
			if err != nil {
				return nil, fmt.Errorf("ensure node: %w", err)
			}
			if _, err := RunCommandLong(ctx, 15*time.Minute, "bash", "-lc",
				fmt.Sprintf("cd %s && %s install --no-audit --no-fund && %s run build",
					shellQuote(webmailSrc), shellQuote(npm), shellQuote(npm))); err != nil {
				return nil, fmt.Errorf("webmail build: %w", err)
			}
		}
		// Copy dist → /opt/mail-suite/webmail. `cp -rT` overwrites the
		// destination dir in-place so re-runs stay idempotent.
		if err := os.MkdirAll(webmailDir, 0755); err != nil {
			return nil, fmt.Errorf("mkdir webmail: %w", err)
		}
		if _, err := RunCommand(ctx, "bash", "-lc",
			fmt.Sprintf("cp -r %s/. %s/", shellQuote(distDir), shellQuote(webmailDir))); err != nil {
			return nil, fmt.Errorf("copy webmail dist: %w", err)
		}
	}

	// 3. .env
	envBody := fmt.Sprintf(`# Auto-generated by Betazen panel — edit with care.
APP_ENV=production
LOG_LEVEL=info

MONGO_URI=%s
MONGO_DB=%s

JWT_SECRET=%s
JWT_ACCESS_EXPIRY=15m
JWT_REFRESH_EXPIRY=168h

SERVER_PORT=9090
PUBLIC_URL=https://%s

IMAP_HOST=127.0.0.1
IMAP_PORT=143
SMTP_HOST=127.0.0.1
SMTP_PORT=587

BETAZEN_PANEL_URL=%s
BETAZEN_PANEL_TOKEN=%s

WEBAUTHN_RPID=%s
WEBAUTHN_RP_NAME=Betazen Mail
WEBAUTHN_ORIGINS=https://%s

OAUTH_ISSUER=https://%s

# Webmail SPA — built dist/ copied next to the binary by the panel
# installer. Backend serves it at /mail/ and redirects / → /mail/.
WEBMAIL_DIR=%s

# Service token used by the Betazen panel to call this mail-suite
# backend's admin endpoints. The panel stores the same value when it
# registers the deployment.
PANEL_SERVICE_TOKEN=%s
`, opts.MongoURI, opts.MongoDBName, opts.JWTSecret,
		opts.Domain, opts.PanelURL, opts.PanelToken,
		opts.Domain, opts.Domain, opts.Domain, webmailDir, opts.ServiceToken)
	if err := os.WriteFile(envPath, []byte(envBody), 0640); err != nil {
		return nil, fmt.Errorf("write .env: %w", err)
	}

	// 4. systemd unit
	unit := fmt.Sprintf(`[Unit]
Description=Betazen Mail Suite
After=network.target mongod.service

[Service]
Type=simple
WorkingDirectory=%s
EnvironmentFile=%s
ExecStart=%s
Restart=on-failure
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
`, opts.InstallDir, envPath, binaryPath)
	if err := os.WriteFile(systemdUnit, []byte(unit), 0644); err != nil {
		return nil, fmt.Errorf("write systemd unit: %w", err)
	}
	RunCommand(ctx, "systemctl", "daemon-reload")
	if _, err := RunCommand(ctx, "systemctl", "enable", "--now", "mail-suite.service"); err != nil {
		return nil, fmt.Errorf("systemctl enable mail-suite: %w", err)
	}

	// 5. nginx vhost (reverse-proxy)
	//
	// Bootstrap TLS material: the certbot --nginx run below swaps in the
	// real Let's Encrypt cert on success, but if it FAILS — DNS for the
	// mail host not public yet, port 80 unreachable, LE rate limit — the
	// 443 block must still reference a cert that EXISTS on disk. Otherwise
	// the enabled vhost points at a non-existent
	// /etc/letsencrypt/live/<d>/fullchain.pem and `nginx -t` fails
	// GLOBALLY, which blocks every other nginx reload on the box (domain
	// creates, SSL issues, suspends — the lot). Use the live LE cert when
	// one already exists; otherwise mint a throwaway self-signed cert so
	// HTTPS serves (with a browser warning) until the real one lands —
	// exactly what the old "serve self-signed until then" comment promised
	// but never actually did.
	certPath := fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", opts.Domain)
	keyPath := fmt.Sprintf("/etc/letsencrypt/live/%s/privkey.pem", opts.Domain)
	if !LetsEncryptCertExists(opts.Domain) {
		sc, sk, err := ensureSelfSignedCert(ctx, opts.Domain)
		if err != nil {
			return nil, fmt.Errorf("bootstrap self-signed cert: %w", err)
		}
		certPath, keyPath = sc, sk
	}
	vhost := fmt.Sprintf(`# Auto-generated by Betazen panel.
server {
    listen 80;
    server_name %s;

    location /.well-known/acme-challenge/ { root /var/www/html; }
    location / { return 301 https://$host$request_uri; }
}

server {
    listen 443 ssl http2;
    server_name %s;

    # certbot --nginx replaces these with the LE cert on success; until
    # then they point at a self-signed bootstrap cert so the block always
    # loads (a missing cert here would break nginx -t for the whole box).
    ssl_certificate     %s;
    ssl_certificate_key %s;

    client_max_body_size 50M;

    location / {
        proxy_pass http://127.0.0.1:9090;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 5m;
    }
}
`, opts.Domain, opts.Domain, certPath, keyPath)
	if err := os.WriteFile(nginxVhost, []byte(vhost), 0644); err != nil {
		return nil, fmt.Errorf("write nginx vhost: %w", err)
	}
	// idempotent symlink
	_ = os.Remove(nginxLink)
	_ = os.Symlink(nginxVhost, nginxLink)

	// 6. certbot (best-effort) — via runCertbotLong so it serializes with
	// every other panel certbot call and retries the transient lock/network
	// failures instead of dying on a concurrent renew-timer run.
	_, _ = runCertbotLong(ctx, 5*time.Minute, "--nginx",
		"-d", opts.Domain, "--non-interactive", "--agree-tos",
		"--register-unsafely-without-email", "--redirect")
	RunCommand(ctx, "nginx", "-t")
	RunCommand(ctx, "systemctl", "reload", "nginx")

	return &MailSuiteInstallResult{
		APIURL:       "https://" + opts.Domain,
		WebmailURL:   "https://" + opts.Domain + "/mail/",
		ServiceToken: opts.ServiceToken,
		InstallDir:   opts.InstallDir,
		BinaryPath:   binaryPath,
		SystemdUnit:  systemdUnit,
		NginxVhost:   nginxVhost,
	}, nil
}

// ensureSelfSignedCert mints a throwaway self-signed certificate for
// `domain` under /etc/ssl/mail-suite/ when one isn't already there and
// returns its cert + key paths. Used as bootstrap TLS material so a 443
// vhost is always loadable by nginx even before — or if — certbot issues
// the real Let's Encrypt cert. Idempotent: reuses an existing pair.
func ensureSelfSignedCert(ctx context.Context, domain string) (string, string, error) {
	dir := "/etc/ssl/mail-suite"
	certPath := filepath.Join(dir, domain+".crt")
	keyPath := filepath.Join(dir, domain+".key")
	if _, e1 := os.Stat(certPath); e1 == nil {
		if _, e2 := os.Stat(keyPath); e2 == nil {
			return certPath, keyPath, nil
		}
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	// openssl is present on every box certbot runs on. -subj keeps it
	// non-interactive; CN alone is enough for nginx to load the cert (it's
	// a bootstrap placeholder, replaced by certbot --nginx on success).
	if _, err := RunCommand(ctx, "openssl", "req", "-x509", "-nodes",
		"-newkey", "rsa:2048",
		"-days", "825",
		"-keyout", keyPath,
		"-out", certPath,
		"-subj", "/CN="+domain,
	); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ensureGo returns an absolute path to a `go` binary whose version is
// at least goBootstrapVersion. Search order:
//
//   1. /usr/local/go/bin/go            — the canonical install location
//   2. `go` on PATH                    — distro / apt-installed
//   3. download + extract from go.dev  — when neither is suitable
//
// Returns the path. Never falls back to a too-old `go`: an old toolchain
// would fail with cryptic "undefined: any" / language-feature errors,
// so we'd rather take the one-time download hit.
func ensureGo(ctx context.Context) (string, error) {
	const minVer = goBootstrapVersion

	// 1. /usr/local/go/bin/go
	primary := "/usr/local/go/bin/go"
	if v, ok := goVersionAtLeast(ctx, primary, minVer); ok {
		return primary, nil
	} else if v != "" {
		// Found but stale — fall through to reinstall.
	}

	// 2. PATH lookup
	if p, err := exec.LookPath("go"); err == nil {
		if _, ok := goVersionAtLeast(ctx, p, minVer); ok {
			return p, nil
		}
	}

	// 3. Download official tarball.
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("unsupported arch %s; please install Go %s manually", arch, minVer)
	}
	tar := fmt.Sprintf("go%s.linux-%s.tar.gz", minVer, arch)
	url := "https://go.dev/dl/" + tar
	tmp := "/tmp/" + tar

	// Pull the tarball; -fL follows the redirect dl.google.com sends.
	if _, err := RunCommandLong(ctx, 5*time.Minute, "curl", "-fL", "-o", tmp, url); err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	// Replace any stale install.
	if _, err := RunCommand(ctx, "rm", "-rf", "/usr/local/go"); err != nil {
		return "", fmt.Errorf("remove old go: %w", err)
	}
	if _, err := RunCommandLong(ctx, 5*time.Minute, "tar", "-C", "/usr/local", "-xzf", tmp); err != nil {
		return "", fmt.Errorf("extract go: %w", err)
	}
	_ = os.Remove(tmp)

	if _, ok := goVersionAtLeast(ctx, primary, minVer); !ok {
		return "", fmt.Errorf("installed Go at %s but version check still failed", primary)
	}
	return primary, nil
}

// goVersionAtLeast runs `<path> version` and returns (versionString, ok)
// where ok = true iff the binary exists, runs, and reports a version
// >= want. The version comparison is lexicographic-on-numeric-segments
// so "1.22.5" >= "1.22.5" and "1.23.0" >= "1.22.5" both hold.
func goVersionAtLeast(ctx context.Context, path, want string) (string, bool) {
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	res, err := RunCommand(ctx, path, "version")
	if err != nil || res == nil {
		return "", false
	}
	// Output: "go version go1.22.5 linux/amd64"
	out := strings.TrimSpace(res.Output)
	parts := strings.Fields(out)
	if len(parts) < 3 || !strings.HasPrefix(parts[2], "go") {
		return out, false
	}
	got := strings.TrimPrefix(parts[2], "go")
	return got, semverAtLeast(got, want)
}

// semverAtLeast compares two dotted-numeric version strings. Trailing
// non-numeric suffixes ("-beta", "rc1") are treated as 0 — fine for the
// fixed pin in goBootstrapVersion which never carries one.
func semverAtLeast(got, want string) bool {
	gs := strings.Split(got, ".")
	ws := strings.Split(want, ".")
	for i := 0; i < len(ws); i++ {
		g := 0
		if i < len(gs) {
			g = parseLeadingInt(gs[i])
		}
		w := parseLeadingInt(ws[i])
		if g > w {
			return true
		}
		if g < w {
			return false
		}
	}
	return true
}

func parseLeadingInt(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// shellQuote wraps a string for safe shell interpolation. Used in the
// `bash -lc` go-build invocation so install dirs with spaces don't
// break the build step.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// findMailSuiteSources locates the in-repo backend and webmail
// directories. Returns ("", "") if neither layout matches; the caller
// decides whether that's fatal.
//
// Search order:
//   1. /opt/server-panel/mail-suite/{backend,webmail}  (deploy layout)
//   2. <cwd>/mail-suite/{backend,webmail}              (dev layout)
func findMailSuiteSources() (backend, webmail string) {
	candidates := []string{"/opt/server-panel"}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	for _, root := range candidates {
		b := filepath.Join(root, "mail-suite", "backend")
		w := filepath.Join(root, "mail-suite", "webmail")
		if _, err := os.Stat(b); err == nil {
			backend = b
			if _, err := os.Stat(w); err == nil {
				webmail = w
			}
			return
		}
	}
	return "", ""
}

// nodeBootstrapMajor is the Node.js LTS we install if the box has none
// or an older version. Vite 5 + the @tiptap stack the webmail uses
// require Node 18+; we pick 20 LTS for the longer support window.
const nodeBootstrapMajor = 20

// ensureNode returns an absolute path to an `npm` binary backed by
// Node.js >= nodeBootstrapMajor. Search order:
//
//   1. `node` + `npm` on PATH (any distro / nvm install)
//   2. NodeSource setup script + apt-get install nodejs
//
// Returns the npm path. We use npm rather than node because the build
// is invoked as `npm install && npm run build`.
func ensureNode(ctx context.Context) (string, error) {
	// 1. PATH lookup with version gate.
	if p, err := exec.LookPath("node"); err == nil {
		if ok := nodeVersionAtLeast(ctx, p, nodeBootstrapMajor); ok {
			if np, err := exec.LookPath("npm"); err == nil {
				return np, nil
			}
		}
	}

	// 2. NodeSource bootstrap. The shell pipeline writes /etc/apt/...
	// sources for the requested major and then apt-get installs.
	// On debian/ubuntu this lands node at /usr/bin/node and npm at
	// /usr/bin/npm.
	script := fmt.Sprintf(
		"curl -fsSL https://deb.nodesource.com/setup_%d.x | bash - && DEBIAN_FRONTEND=noninteractive apt-get install -y nodejs",
		nodeBootstrapMajor,
	)
	if _, err := RunCommandLong(ctx, 10*time.Minute, "bash", "-lc", script); err != nil {
		return "", fmt.Errorf("install node via NodeSource: %w", err)
	}
	np, err := exec.LookPath("npm")
	if err != nil {
		return "", fmt.Errorf("npm still not on PATH after install: %w", err)
	}
	return np, nil
}

// nodeVersionAtLeast runs `<node> --version` (output: "v20.11.1") and
// returns true iff the major is >= want.
func nodeVersionAtLeast(ctx context.Context, path string, want int) bool {
	res, err := RunCommand(ctx, path, "--version")
	if err != nil || res == nil {
		return false
	}
	out := strings.TrimSpace(res.Output)
	out = strings.TrimPrefix(out, "v")
	dot := strings.IndexByte(out, '.')
	if dot < 0 {
		return false
	}
	return parseLeadingInt(out[:dot]) >= want
}
