package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
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
}

func CreateVhost(ctx context.Context, cfg *VhostConfig) error {
	tmpl, err := template.New("vhost").Parse(vhostTemplate)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return err
	}

	path := fmt.Sprintf("/etc/nginx/sites-available/%s", cfg.Domain)
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return err
	}

	link := fmt.Sprintf("/etc/nginx/sites-enabled/%s", cfg.Domain)
	_ = os.Remove(link)
	if err := os.Symlink(path, link); err != nil {
		return err
	}

	return ReloadNginx(ctx)
}

func CreateReverseProxy(ctx context.Context, cfg *VhostConfig) error {
	tmpl, err := template.New("proxy").Parse(reverseProxyTemplate)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return err
	}

	path := fmt.Sprintf("/etc/nginx/sites-available/%s", cfg.Domain)
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		return err
	}

	link := fmt.Sprintf("/etc/nginx/sites-enabled/%s", cfg.Domain)
	_ = os.Remove(link)
	if err := os.Symlink(path, link); err != nil {
		return err
	}

	return ReloadNginx(ctx)
}

func DeleteVhost(ctx context.Context, domain string) error {
	os.Remove(fmt.Sprintf("/etc/nginx/sites-enabled/%s", domain))
	os.Remove(fmt.Sprintf("/etc/nginx/sites-available/%s", domain))
	return ReloadNginx(ctx)
}

func ReloadNginx(ctx context.Context) error {
	if _, err := RunCommand(ctx, "nginx", "-t"); err != nil {
		return fmt.Errorf("nginx config test failed: %w", err)
	}
	_, err := RunCommand(ctx, "systemctl", "reload", "nginx")
	return err
}

// ForceSSL enables or disables HTTP-to-HTTPS redirect for a domain.
// It modifies the port 80 server block in the nginx config.
func ForceSSL(ctx context.Context, domain string, enable bool) error {
	confPath := fmt.Sprintf("/etc/nginx/sites-available/%s", domain)
	data, err := os.ReadFile(confPath)
	if err != nil {
		return fmt.Errorf("failed to read nginx config: %w", err)
	}

	content := string(data)
	redirect := "    return 301 https://$host$request_uri;"

	if enable {
		if strings.Contains(content, "return 301 https://") {
			return nil // already enabled
		}
		// Insert redirect after the first "listen 80;" line
		lines := strings.Split(content, "\n")
		var result []string
		inserted := false
		for _, line := range lines {
			result = append(result, line)
			if !inserted && strings.Contains(strings.TrimSpace(line), "listen 80") {
				// Find server_name line next, then insert redirect after it
				continue
			}
			if !inserted && strings.HasPrefix(strings.TrimSpace(line), "server_name ") {
				result = append(result, redirect)
				inserted = true
			}
		}
		content = strings.Join(result, "\n")
	} else {
		// Remove the redirect line
		lines := strings.Split(content, "\n")
		var result []string
		for _, line := range lines {
			if strings.TrimSpace(line) != strings.TrimSpace(redirect) {
				result = append(result, line)
			}
		}
		content = strings.Join(result, "\n")
	}

	if err := os.WriteFile(confPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write nginx config: %w", err)
	}

	return ReloadNginx(ctx)
}

func TestNginxConfig(ctx context.Context) (string, error) {
	result, err := RunCommand(ctx, "nginx", "-t")
	if err != nil {
		return result.Error, err
	}
	return "nginx: configuration file test is successful", nil
}
