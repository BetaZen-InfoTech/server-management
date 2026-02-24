package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"text/template"
)

const vhostTemplate = `server {
    listen 80;
    server_name {{.Domain}} www.{{.Domain}};
    root /home/{{.User}}/public_html;
    index index.php index.html;

    access_log /var/log/nginx/{{.Domain}}-access.log;
    error_log /var/log/nginx/{{.Domain}}-error.log;

    location / {
        try_files $uri $uri/ /index.php?$query_string;
    }

    location ~ \.php$ {
        fastcgi_pass unix:/run/php/php{{.PHPVersion}}-fpm-{{.User}}.sock;
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

func TestNginxConfig(ctx context.Context) (string, error) {
	result, err := RunCommand(ctx, "nginx", "-t")
	if err != nil {
		return result.Error, err
	}
	return "nginx: configuration file test is successful", nil
}
