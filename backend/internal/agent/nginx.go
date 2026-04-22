package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"text/template"
)

// AcmeChallengeRoot is the webroot certbot writes HTTP-01 challenges
// into. Every vhost template serves /.well-known/acme-challenge/ from
// here so a certbot --webroot run can satisfy the challenge without
// touching the vhost config. install.sh creates this directory during
// bootstrap; see also AcmeChallengeLocation below.
const AcmeChallengeRoot = "/var/www/certbot"

// acmeChallengeLocation is the nginx location block every :80 server
// block includes so certbot --webroot works uniformly. Placed before
// the redirect / PHP / proxy locations so ACME traffic never gets
// redirected to https (LE validators don't follow redirects to https
// reliably on retries) or handed to an FPM upstream that doesn't exist
// yet.
const acmeChallengeLocation = `    location ^~ /.well-known/acme-challenge/ {
        root ` + AcmeChallengeRoot + `;
        default_type "text/plain";
        try_files $uri =404;
    }

`

const vhostTemplate = `server {
    listen 80;
    server_name {{.Domain}} www.{{.Domain}};
    root /home/{{.User}}/domains/{{.Domain}}/public_html;
    index index.php index.html;

    access_log /var/log/nginx/{{.Domain}}-access.log;
    error_log /var/log/nginx/{{.Domain}}-error.log;

` + acmeChallengeLocation + `    location / {
        try_files $uri $uri/ /index.php?$query_string;
    }

    location ~ \.php$ {
        fastcgi_pass unix:/run/php/php{{.PHPVersion}}-fpm-{{.Domain}}.sock;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        include fastcgi_params;
    }

    location ~ /\.ht {
        deny all;
    }
}
`

const vhostSSLTemplate = `server {
    listen 80;
    server_name {{.Domain}} www.{{.Domain}};

` + acmeChallengeLocation + `    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl;
    server_name {{.Domain}} www.{{.Domain}};
    root /home/{{.User}}/domains/{{.Domain}}/public_html;
    index index.php index.html;

    ssl_certificate {{.CertPath}};
    ssl_certificate_key {{.KeyPath}};

    access_log /var/log/nginx/{{.Domain}}-access.log;
    error_log /var/log/nginx/{{.Domain}}-error.log;

    location / {
        try_files $uri $uri/ /index.php?$query_string;
    }

    location ~ \.php$ {
        fastcgi_pass unix:/run/php/php{{.PHPVersion}}-fpm-{{.Domain}}.sock;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        include fastcgi_params;
    }

    location ~ /\.ht {
        deny all;
    }
}
`

const reverseProxyTemplate = `server {
    listen 80;
    server_name {{.Domain}};

` + acmeChallengeLocation + `    location / {
        proxy_pass http://127.0.0.1:{{.Port}};
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 86400;
    }
}
`

const reverseProxySSLTemplate = `server {
    listen 80;
    server_name {{.Domain}};

` + acmeChallengeLocation + `    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl;
    server_name {{.Domain}};

    ssl_certificate {{.CertPath}};
    ssl_certificate_key {{.KeyPath}};

    location / {
        proxy_pass http://127.0.0.1:{{.Port}};
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 86400;
    }
}
`

type VhostConfig struct {
	Domain     string
	User       string
	PHPVersion string
	Port       int
	CertPath   string // SSL certificate path (defaults to LE path if empty)
	KeyPath    string // SSL private key path (defaults to LE path if empty)
}

// writeVhostConfig writes an nginx config file and creates the symlink using shell commands.
// Returns the sites-available path and sites-enabled path.
func writeVhostConfig(ctx context.Context, domain string, content []byte) (string, string, error) {
	availPath := fmt.Sprintf("/etc/nginx/sites-available/%s", domain)
	enabledPath := fmt.Sprintf("/etc/nginx/sites-enabled/%s", domain)

	// Write config file using tee (works regardless of process user permissions)
	cmd := fmt.Sprintf("cat > '%s'", availPath)
	writeCmd := fmt.Sprintf("echo '%s' | tee '%s' > /dev/null", strings.ReplaceAll(string(content), "'", "'\\''"), availPath)
	// Use bash -c with heredoc for safe content writing
	_ = cmd
	RunCommand(ctx, "bash", "-c", fmt.Sprintf("cat > '%s' << 'NGINX_EOF'\n%s\nNGINX_EOF", availPath, string(content)))

	// Remove old symlink and create new one
	RunCommand(ctx, "rm", "-f", enabledPath)
	RunCommand(ctx, "ln", "-sf", availPath, enabledPath)

	_ = writeCmd
	return availPath, enabledPath, nil
}

func CreateVhost(ctx context.Context, cfg *VhostConfig) error {
	cfg.User = strings.TrimSpace(cfg.User)
	cfg.Domain = strings.TrimSpace(cfg.Domain)

	// Pre-cleanup: remove any leftover configs for this domain
	cleanupVhostFiles(ctx, cfg.Domain)

	tmpl, err := template.New("vhost").Parse(vhostTemplate)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return err
	}

	availPath, enabledPath, err := writeVhostConfig(ctx, cfg.Domain, buf.Bytes())
	if err != nil {
		return err
	}

	if err := ReloadNginx(ctx); err != nil {
		// Clean up broken config so it doesn't poison future nginx operations
		RunCommand(ctx, "rm", "-f", enabledPath, availPath)
		return err
	}
	return nil
}

// CreateVhostWithSSL writes the SSL-enabled nginx config (port 80 redirect + port 443 block).
// If CertPath/KeyPath are empty, defaults to Let's Encrypt paths.
func CreateVhostWithSSL(ctx context.Context, cfg *VhostConfig) error {
	cfg.User = strings.TrimSpace(cfg.User)
	cfg.Domain = strings.TrimSpace(cfg.Domain)

	// Default to Let's Encrypt certificate paths
	if cfg.CertPath == "" {
		cfg.CertPath = fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", cfg.Domain)
	}
	if cfg.KeyPath == "" {
		cfg.KeyPath = fmt.Sprintf("/etc/letsencrypt/live/%s/privkey.pem", cfg.Domain)
	}

	tmpl, err := template.New("vhost-ssl").Parse(vhostSSLTemplate)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return err
	}

	availPath, enabledPath, err := writeVhostConfig(ctx, cfg.Domain, buf.Bytes())
	if err != nil {
		return err
	}

	if err := ReloadNginx(ctx); err != nil {
		RunCommand(ctx, "rm", "-f", enabledPath, availPath)
		return err
	}
	return nil
}

// CreateStaticVhost writes an nginx config that serves a directory as a
// static site with SPA fallback. Used for static frameworks (React, Vite,
// plain HTML) where no backend service is required.
func CreateStaticVhost(ctx context.Context, domain, rootDir string) error {
	domain = strings.TrimSpace(domain)
	if domain == "" || rootDir == "" {
		return fmt.Errorf("domain and root are required")
	}
	cleanupVhostFiles(ctx, domain)

	content := fmt.Sprintf(`server {
    listen 80;
    server_name %s;
    root %s;
    index index.html;

    access_log /var/log/nginx/%s-access.log;
    error_log /var/log/nginx/%s-error.log;

%s    location / {
        try_files $uri $uri/ /index.html;
    }
}
`, domain, rootDir, domain, domain, acmeChallengeLocation)

	availPath, enabledPath, err := writeVhostConfig(ctx, domain, []byte(content))
	if err != nil {
		return err
	}
	if err := ReloadNginx(ctx); err != nil {
		RunCommand(ctx, "rm", "-f", enabledPath, availPath)
		return err
	}
	return nil
}

// CreateStaticVhostWithSSL writes an nginx config that serves a directory
// statically, with a 80→443 redirect and a 443 server block. CertPath/KeyPath
// default to the canonical Let's Encrypt paths when empty.
func CreateStaticVhostWithSSL(ctx context.Context, domain, rootDir, certPath, keyPath string) error {
	domain = strings.TrimSpace(domain)
	if domain == "" || rootDir == "" {
		return fmt.Errorf("domain and root are required")
	}
	if certPath == "" {
		certPath = fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", domain)
	}
	if keyPath == "" {
		keyPath = fmt.Sprintf("/etc/letsencrypt/live/%s/privkey.pem", domain)
	}
	cleanupVhostFiles(ctx, domain)

	content := fmt.Sprintf(`server {
    listen 80;
    server_name %s;

%s    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl;
    server_name %s;
    root %s;
    index index.html;

    ssl_certificate %s;
    ssl_certificate_key %s;

    access_log /var/log/nginx/%s-access.log;
    error_log /var/log/nginx/%s-error.log;

    location / {
        try_files $uri $uri/ /index.html;
    }
}
`, domain, acmeChallengeLocation, domain, rootDir, certPath, keyPath, domain, domain)

	availPath, enabledPath, err := writeVhostConfig(ctx, domain, []byte(content))
	if err != nil {
		return err
	}
	if err := ReloadNginx(ctx); err != nil {
		RunCommand(ctx, "rm", "-f", enabledPath, availPath)
		return err
	}
	return nil
}

// CreateReverseProxyWithSSL writes a reverse-proxy nginx config with a
// 80→443 redirect and a 443 server block. CertPath/KeyPath default to the
// canonical Let's Encrypt paths when empty.
func CreateReverseProxyWithSSL(ctx context.Context, cfg *VhostConfig) error {
	cfg.Domain = strings.TrimSpace(cfg.Domain)
	if cfg.CertPath == "" {
		cfg.CertPath = fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", cfg.Domain)
	}
	if cfg.KeyPath == "" {
		cfg.KeyPath = fmt.Sprintf("/etc/letsencrypt/live/%s/privkey.pem", cfg.Domain)
	}
	cleanupVhostFiles(ctx, cfg.Domain)

	tmpl, err := template.New("proxy-ssl").Parse(reverseProxySSLTemplate)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return err
	}
	availPath, enabledPath, err := writeVhostConfig(ctx, cfg.Domain, buf.Bytes())
	if err != nil {
		return err
	}
	if err := ReloadNginx(ctx); err != nil {
		RunCommand(ctx, "rm", "-f", enabledPath, availPath)
		return err
	}
	return nil
}

// LetsEncryptCertExists reports whether a Let's Encrypt certificate already
// lives on disk for the given domain. Used by deploy flows to decide between
// the HTTP-only and SSL vhost templates without round-tripping through
// certbot.
func LetsEncryptCertExists(domain string) bool {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return false
	}
	path := fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", domain)
	_, err := os.Stat(path)
	return err == nil
}

func CreateReverseProxy(ctx context.Context, cfg *VhostConfig) error {
	cfg.Domain = strings.TrimSpace(cfg.Domain)

	// Pre-cleanup
	cleanupVhostFiles(ctx, cfg.Domain)

	tmpl, err := template.New("proxy").Parse(reverseProxyTemplate)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return err
	}

	availPath, enabledPath, err := writeVhostConfig(ctx, cfg.Domain, buf.Bytes())
	if err != nil {
		return err
	}

	if err := ReloadNginx(ctx); err != nil {
		RunCommand(ctx, "rm", "-f", enabledPath, availPath)
		return err
	}
	return nil
}

// cleanupVhostFiles removes nginx config files for a specific domain without
// touching unrelated vhosts. The previous implementation used
//   find -name '*<domain>*'
// which matched as a substring — deploying an app at "app.local" would wipe
// every vhost whose filename contained ".local". We now only remove the
// exact-name files and broken symlinks.
func cleanupVhostFiles(ctx context.Context, domain string) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return
	}
	availPath := fmt.Sprintf("/etc/nginx/sites-available/%s", domain)
	enabledPath := fmt.Sprintf("/etc/nginx/sites-enabled/%s", domain)
	RunCommand(ctx, "rm", "-f", availPath, enabledPath)
	// Also clean up any stale "<domain> " / "<domain>.bak" style artefacts
	// from older broken write attempts, but anchor the glob so it only
	// matches names that *start* with the exact domain plus a delimiter.
	RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		`for d in /etc/nginx/sites-enabled /etc/nginx/sites-available; do
  for f in "$d/%s "* "$d/%s."* ; do
    [ -e "$f" ] && rm -f "$f"
  done
done 2>/dev/null`, domain, domain))
}

func DeleteVhost(ctx context.Context, domain string) error {
	cleanupVhostFiles(ctx, domain)
	// Reload nginx; ignore errors since config might already be broken
	RunCommand(ctx, "bash", "-c", "nginx -t 2>/dev/null && systemctl reload nginx 2>/dev/null")
	return nil
}

// placeholderDocRoot is the doc-root used by WritePlaceholderVhost. It
// holds a single index.html that every placeholder vhost serves. Kept
// in /var/www/ so it survives nginx restarts and isn't tangled with any
// tenant's home dir.
const placeholderDocRoot = "/var/www/sp-placeholder"

// ensurePlaceholderDocRoot creates /var/www/sp-placeholder/index.html on
// first use. Idempotent — safe to call from every Delete.
func ensurePlaceholderDocRoot(ctx context.Context) {
	RunCommand(ctx, "install", "-d", "-m", "0755", placeholderDocRoot)
	indexPath := placeholderDocRoot + "/index.html"
	if _, err := os.Stat(indexPath); err == nil {
		return
	}
	body := `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Site not deployed</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
body{font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;background:#0f172a;color:#e2e8f0;margin:0;display:flex;min-height:100vh;align-items:center;justify-content:center}
.card{max-width:560px;padding:40px;text-align:center}
h1{margin:0 0 12px;font-size:22px;font-weight:600}
p{margin:0 0 8px;color:#94a3b8;line-height:1.6;font-size:14px}
.foot{margin-top:24px;font-size:12px;color:#475569}
</style>
</head>
<body>
<div class="card">
<h1>Site not deployed</h1>
<p>No application is currently served at this domain.</p>
<p>If this is your domain, deploy an app from the WHM panel or remove the domain entirely.</p>
<p class="foot">Betazen Server Panel</p>
</div>
</body>
</html>
`
	os.WriteFile(indexPath, []byte(body), 0644)
}

// suspendedDocRoot holds the single index.html that every suspended
// vhost renders. Separate from placeholderDocRoot so the two flows
// produce visibly different pages (the operator should be able to
// tell at a glance whether a site is suspended vs merely undeployed).
const suspendedDocRoot = "/var/www/sp-suspended"

// ensureSuspendedDocRoot creates /var/www/sp-suspended/index.html on
// first use. Idempotent — safe to call from every Suspend.
func ensureSuspendedDocRoot(ctx context.Context) {
	RunCommand(ctx, "install", "-d", "-m", "0755", suspendedDocRoot)
	indexPath := suspendedDocRoot + "/index.html"
	if _, err := os.Stat(indexPath); err == nil {
		return
	}
	body := `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Account suspended</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
body{font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;background:#0f172a;color:#e2e8f0;margin:0;display:flex;min-height:100vh;align-items:center;justify-content:center}
.card{max-width:560px;padding:40px;text-align:center}
.badge{display:inline-block;padding:4px 10px;border-radius:999px;background:#7f1d1d;color:#fecaca;font-size:11px;letter-spacing:.08em;text-transform:uppercase;margin-bottom:18px}
h1{margin:0 0 12px;font-size:22px;font-weight:600}
p{margin:0 0 10px;color:#94a3b8;line-height:1.6;font-size:14px}
.foot{margin-top:24px;font-size:12px;color:#475569}
</style>
</head>
<body>
<div class="card">
<span class="badge">503 Service Unavailable</span>
<h1>This account is suspended</h1>
<p>The owner of this website has had their account suspended by the hosting provider.</p>
<p>If you are the owner, please contact your hosting provider to resolve the suspension.</p>
<p class="foot">Betazen Server Panel</p>
</div>
</body>
</html>
`
	os.WriteFile(indexPath, []byte(body), 0644)
}

// WriteSuspendedVhost replaces a domain's nginx config with a 503
// response that renders the "account suspended" page. Mirrors
// WritePlaceholderVhost but uses an HTTP 503 status (so crawlers know
// to retry later) + a visibly red "suspended" badge so visitors can
// tell this isn't merely a missing site.
//
// Keeps the domain's OWN server_name + cert binding so the browser
// doesn't show a cert-name-mismatch warning before the body renders.
// The vhost file stays at /etc/nginx/sites-available/<domain> so
// unsuspend can rewrite it back to the live site in one step.
func WriteSuspendedVhost(ctx context.Context, domain string) error {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return nil
	}
	ensureSuspendedDocRoot(ctx)
	cleanupVhostFiles(ctx, domain)

	hasSSL := LetsEncryptCertExists(domain)
	var content string
	if hasSSL {
		content = fmt.Sprintf(`server {
    listen 80;
    server_name %s www.%s;

%s    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl;
    server_name %s www.%s;

    ssl_certificate /etc/letsencrypt/live/%s/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/%s/privkey.pem;

    access_log /var/log/nginx/%s-access.log;
    error_log /var/log/nginx/%s-error.log;

    root %s;
    index index.html;

    # Every path returns 503 with the suspended page body.
    location / {
        error_page 503 /index.html;
        return 503;
    }
    # The error_page target itself must be servable without re-entering
    # the 503 branch, otherwise nginx loops. Match /index.html directly.
    location = /index.html {
        internal;
    }
}
`, domain, domain, acmeChallengeLocation, domain, domain, domain, domain, domain, domain, suspendedDocRoot)
	} else {
		content = fmt.Sprintf(`server {
    listen 80;
    server_name %s www.%s;

    access_log /var/log/nginx/%s-access.log;
    error_log /var/log/nginx/%s-error.log;

%s    root %s;
    index index.html;

    location / {
        error_page 503 /index.html;
        return 503;
    }
    location = /index.html {
        internal;
    }
}
`, domain, domain, domain, domain, acmeChallengeLocation, suspendedDocRoot)
	}

	availPath, enabledPath, err := writeVhostConfig(ctx, domain, []byte(content))
	if err != nil {
		return err
	}
	if err := ReloadNginx(ctx); err != nil {
		RunCommand(ctx, "rm", "-f", enabledPath, availPath)
		RunCommand(ctx, "systemctl", "reload", "nginx")
		return err
	}
	return nil
}

// WritePlaceholderVhost replaces a domain's nginx config with a minimal
// "site not deployed" page. Used by Delete flows so the domain stops
// serving content but keeps its OWN server_name + SSL cert binding.
//
// Without this, deleting a vhost makes nginx fall back to whatever 443
// server block sorts first alphabetically — so https://d2.example.com
// would silently start serving d1.example.com's cert and produce a
// browser cert-name-mismatch error (NET::ERR_CERT_COMMON_NAME_INVALID).
// With this placeholder, the domain keeps a sensible identity (its own
// cert, its own page) until the operator either re-deploys an app there
// or manually removes the vhost file from /etc/nginx/sites-available.
func WritePlaceholderVhost(ctx context.Context, domain string) error {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return nil
	}
	ensurePlaceholderDocRoot(ctx)
	cleanupVhostFiles(ctx, domain)

	hasSSL := LetsEncryptCertExists(domain)
	var content string
	if hasSSL {
		content = fmt.Sprintf(`server {
    listen 80;
    server_name %s;

%s    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl;
    server_name %s;

    ssl_certificate /etc/letsencrypt/live/%s/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/%s/privkey.pem;

    access_log /var/log/nginx/%s-access.log;
    error_log /var/log/nginx/%s-error.log;

    root %s;
    index index.html;
    location / {
        try_files $uri /index.html =410;
    }
}
`, domain, acmeChallengeLocation, domain, domain, domain, domain, domain, placeholderDocRoot)
	} else {
		content = fmt.Sprintf(`server {
    listen 80;
    server_name %s;

    access_log /var/log/nginx/%s-access.log;
    error_log /var/log/nginx/%s-error.log;

%s    root %s;
    index index.html;
    location / {
        try_files $uri /index.html =410;
    }
}
`, domain, domain, domain, acmeChallengeLocation, placeholderDocRoot)
	}

	availPath, enabledPath, err := writeVhostConfig(ctx, domain, []byte(content))
	if err != nil {
		return err
	}
	if err := ReloadNginx(ctx); err != nil {
		// Fall back to fully removing the broken vhost so nginx still reloads cleanly.
		RunCommand(ctx, "rm", "-f", enabledPath, availPath)
		RunCommand(ctx, "systemctl", "reload", "nginx")
		return err
	}
	return nil
}

func ReloadNginx(ctx context.Context) error {
	if _, err := RunCommand(ctx, "nginx", "-t"); err != nil {
		return fmt.Errorf("nginx config test failed: %w", err)
	}
	_, err := RunCommand(ctx, "systemctl", "reload", "nginx")
	return err
}

// ForceSSL enables or disables HTTP-to-HTTPS redirect for a domain.
func ForceSSL(ctx context.Context, domain string, enable bool) error {
	confPath := fmt.Sprintf("/etc/nginx/sites-available/%s", domain)
	result, err := RunCommand(ctx, "cat", confPath)
	if err != nil {
		return fmt.Errorf("failed to read nginx config: %w", err)
	}

	content := result.Output
	redirect := "    return 301 https://$host$request_uri;"

	if enable {
		if strings.Contains(content, "return 301 https://") {
			return nil // already enabled
		}
		lines := strings.Split(content, "\n")
		var resultLines []string
		inserted := false
		for _, line := range lines {
			resultLines = append(resultLines, line)
			if !inserted && strings.Contains(strings.TrimSpace(line), "listen 80") {
				continue
			}
			if !inserted && strings.HasPrefix(strings.TrimSpace(line), "server_name ") {
				resultLines = append(resultLines, redirect)
				inserted = true
			}
		}
		content = strings.Join(resultLines, "\n")
	} else {
		lines := strings.Split(content, "\n")
		var resultLines []string
		for _, line := range lines {
			if strings.TrimSpace(line) != strings.TrimSpace(redirect) {
				resultLines = append(resultLines, line)
			}
		}
		content = strings.Join(resultLines, "\n")
	}

	// Write using bash heredoc
	RunCommand(ctx, "bash", "-c", fmt.Sprintf("cat > '%s' << 'NGINX_EOF'\n%s\nNGINX_EOF", confPath, content))

	return ReloadNginx(ctx)
}

func TestNginxConfig(ctx context.Context) (string, error) {
	result, err := RunCommand(ctx, "nginx", "-t")
	if err != nil {
		return result.Error, err
	}
	return "nginx: configuration file test is successful", nil
}

// ProjectVhostSpec describes a single nginx vhost for a Deploy Software
// project. One vhost = one primary domain + any number of aliases sharing
// the same cert (via certbot --expand). A vhost can mount:
//   - one static root (frontend), and/or
//   - one or more reverse-proxy locations (backend on /api, etc.)
//
// Either Root or at least one Proxy must be set — callers validate upstream.
type ProjectVhostSpec struct {
	PrimaryDomain string
	Aliases       []string
	Root          string            // static root dir (empty if no static frontend)
	Proxies       []ProjectProxyLoc // reverse-proxy locations
	CertPath      string            // SSL cert (default: LE path for PrimaryDomain)
	KeyPath       string            // SSL key  (default: LE path for PrimaryDomain)
	UseSSL        bool              // true once cert exists
}

// ProjectProxyLoc is one reverse-proxy location in a project vhost. Prefix of
// "" or "/" means the backend owns the whole request root.
type ProjectProxyLoc struct {
	Prefix string // e.g. "/" or "/api"
	Port   int
}

// CreateProjectVhost writes the nginx vhost for a Deploy Software project
// service. Emits one "server{}" block on :80 (and :443 with a 80→443 redirect
// when UseSSL is true). server_name lists primary + every alias so a single
// cert covers them all.
func CreateProjectVhost(ctx context.Context, spec *ProjectVhostSpec) error {
	spec.PrimaryDomain = strings.TrimSpace(spec.PrimaryDomain)
	if spec.PrimaryDomain == "" {
		return fmt.Errorf("primary_domain is required")
	}
	if spec.Root == "" && len(spec.Proxies) == 0 {
		return fmt.Errorf("project vhost needs a root or at least one proxy")
	}
	if spec.UseSSL {
		if spec.CertPath == "" {
			spec.CertPath = fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", spec.PrimaryDomain)
		}
		if spec.KeyPath == "" {
			spec.KeyPath = fmt.Sprintf("/etc/letsencrypt/live/%s/privkey.pem", spec.PrimaryDomain)
		}
	}

	names := append([]string{spec.PrimaryDomain}, spec.Aliases...)
	serverNames := strings.Join(names, " ")

	// Build the common location blocks once; reused in both the :80 and :443
	// server blocks (the :80 block is used only when UseSSL is false).
	var locations strings.Builder
	// Backend locations first — prefixes must be matched before the fallback "/".
	for _, p := range spec.Proxies {
		pfx := strings.TrimSpace(p.Prefix)
		if pfx == "" || pfx == "/" {
			pfx = "/"
		}
		fmt.Fprintf(&locations, `    location %s {
        proxy_pass http://127.0.0.1:%d;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 86400;
    }
`, pfx, p.Port)
	}
	if spec.Root != "" {
		// Static fallback only if not already owned by a "/" proxy.
		ownedByProxy := false
		for _, p := range spec.Proxies {
			if p.Prefix == "" || p.Prefix == "/" {
				ownedByProxy = true
				break
			}
		}
		if !ownedByProxy {
			fmt.Fprintf(&locations, `    location / {
        root %s;
        try_files $uri $uri/ /index.html;
    }
`, spec.Root)
		}
	}

	var content strings.Builder
	if spec.UseSSL {
		fmt.Fprintf(&content, `server {
    listen 80;
    server_name %s;

%s    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl;
    server_name %s;

    ssl_certificate %s;
    ssl_certificate_key %s;

    access_log /var/log/nginx/%s-access.log;
    error_log /var/log/nginx/%s-error.log;

%s}
`, serverNames, acmeChallengeLocation, serverNames, spec.CertPath, spec.KeyPath, spec.PrimaryDomain, spec.PrimaryDomain, locations.String())
	} else {
		fmt.Fprintf(&content, `server {
    listen 80;
    server_name %s;

    access_log /var/log/nginx/%s-access.log;
    error_log /var/log/nginx/%s-error.log;

%s%s}
`, serverNames, spec.PrimaryDomain, spec.PrimaryDomain, acmeChallengeLocation, locations.String())
	}

	cleanupVhostFiles(ctx, spec.PrimaryDomain)
	// Also clean up aliases — a previous standalone vhost for an alias would
	// now be a duplicate server_name and make nginx warn / refuse to reload.
	for _, a := range spec.Aliases {
		cleanupVhostFiles(ctx, a)
	}

	availPath, enabledPath, err := writeVhostConfig(ctx, spec.PrimaryDomain, []byte(content.String()))
	if err != nil {
		return err
	}
	if err := ReloadNginx(ctx); err != nil {
		RunCommand(ctx, "rm", "-f", enabledPath, availPath)
		return err
	}
	return nil
}

