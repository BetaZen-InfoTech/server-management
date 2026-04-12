package agent

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/template"
)

const vhostTemplate = `server {
    listen 80;
    server_name {{.Domain}} www.{{.Domain}};
    root /home/{{.User}}/domains/{{.Domain}}/public_html;
    index index.php index.html;

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

const vhostSSLTemplate = `server {
    listen 80;
    server_name {{.Domain}} www.{{.Domain}};
    return 301 https://$host$request_uri;
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

    location / {
        try_files $uri $uri/ /index.html;
    }
}
`, domain, rootDir, domain, domain)

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
