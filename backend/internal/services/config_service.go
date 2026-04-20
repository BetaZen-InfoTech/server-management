package services

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ConfigService struct {
	db *mongo.Database
}

func NewConfigService(db *mongo.Database) *ConfigService {
	return &ConfigService{db: db}
}

// GetAll returns all server configuration sections (nginx, PHP, MongoDB, hostname).
func (s *ConfigService) GetAll(ctx context.Context) (map[string]interface{}, error) {
	col := s.db.Collection(database.ColServerConfig)
	cursor, err := col.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	configs := make(map[string]interface{})
	var docs []bson.M
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	for _, doc := range docs {
		if key, ok := doc["key"].(string); ok {
			configs[key] = doc["value"]
		}
	}

	// Get current hostname if not in DB
	if _, ok := configs["hostname"]; !ok {
		if result, err := agent.RunCommand(ctx, "hostname", "-f"); err == nil {
			configs["hostname"] = strings.TrimSpace(result.Output)
		}
	}

	// Get current timezone if not in DB
	if _, ok := configs["timezone"]; !ok {
		if result, err := agent.RunCommand(ctx, "timedatectl", "show", "--property=Timezone", "--value"); err == nil {
			configs["timezone"] = strings.TrimSpace(result.Output)
		}
	}

	return configs, nil
}

// UpdateNginx applies updated Nginx configuration settings.
func (s *ConfigService) UpdateNginx(ctx context.Context, config *models.NginxConfig) error {
	// Store in DB
	col := s.db.Collection(database.ColServerConfig)
	_, err := col.UpdateOne(ctx,
		bson.M{"key": "nginx"},
		bson.M{"$set": bson.M{"key": "nginx", "value": config, "updated_at": time.Now()}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return err
	}

	// Generate nginx.conf from template
	nginxConf := fmt.Sprintf(`worker_processes %s;
events {
    worker_connections %d;
}
http {
    include /etc/nginx/mime.types;
    default_type application/octet-stream;
    sendfile on;
    keepalive_timeout %d;
    client_max_body_size %s;
    server_tokens %s;
`,
		config.WorkerProcesses,
		config.WorkerConnections,
		config.KeepaliveTimeout,
		config.ClientMaxBodySize,
		boolToOnOff(!config.ServerTokens),
	)

	if config.Gzip {
		nginxConf += "    gzip on;\n"
		if len(config.GzipTypes) > 0 {
			nginxConf += fmt.Sprintf("    gzip_types %s;\n", strings.Join(config.GzipTypes, " "))
		}
	}

	nginxConf += `    include /etc/nginx/conf.d/*.conf;
    include /etc/nginx/sites-enabled/*;
}
`

	// Write config
	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo '%s' > /etc/nginx/nginx.conf", nginxConf)); err != nil {
		return fmt.Errorf("failed to write nginx config: %w", err)
	}

	// Test config
	if _, err := agent.TestNginxConfig(ctx); err != nil {
		// Rollback - let nginx keep running with old config
		return fmt.Errorf("nginx config test failed: %w", err)
	}

	return agent.ReloadNginx(ctx)
}

// UpdatePHP applies updated PHP-FPM configuration settings.
func (s *ConfigService) UpdatePHP(ctx context.Context, config *models.PHPConfig) error {
	col := s.db.Collection(database.ColServerConfig)
	_, err := col.UpdateOne(ctx,
		bson.M{"key": "php"},
		bson.M{"$set": bson.M{"key": "php", "value": config, "updated_at": time.Now()}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return err
	}

	// Generate php.ini settings
	displayErrors := "Off"
	if config.DisplayErrors {
		displayErrors = "On"
	}
	opcacheEnabled := "0"
	if config.OpcacheEnabled {
		opcacheEnabled = "1"
	}

	phpIni := fmt.Sprintf(`memory_limit = %s
max_execution_time = %d
max_input_time = %d
post_max_size = %s
upload_max_filesize = %s
max_file_uploads = %d
display_errors = %s
error_reporting = %s
date.timezone = %s
opcache.enable = %s
opcache.memory_consumption = %d
`,
		config.MemoryLimit, config.MaxExecutionTime, config.MaxInputTime,
		config.PostMaxSize, config.UploadMaxFilesize, config.MaxFileUploads,
		displayErrors, config.ErrorReporting, config.DateTimezone,
		opcacheEnabled, config.OpcacheMemory,
	)

	// Find PHP version and write config
	if result, err := agent.RunCommand(ctx, "bash", "-c", "ls /etc/php/*/fpm/conf.d/ -d 2>/dev/null | head -1"); err == nil {
		confDir := strings.TrimSpace(result.Output)
		if confDir != "" {
			agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo '%s' > %s/99-custom.ini", phpIni, confDir))
		}
	}

	// Reload PHP-FPM
	if result, err := agent.RunCommand(ctx, "bash", "-c", "systemctl list-units --type=service --plain | grep php | awk '{print $1}' | head -1"); err == nil {
		svc := strings.TrimSpace(result.Output)
		if svc != "" {
			agent.ServiceAction(ctx, strings.TrimSuffix(svc, ".service"), "reload")
		}
	}

	return nil
}

// UpdateMongoDB applies updated MongoDB configuration settings.
func (s *ConfigService) UpdateMongoDB(ctx context.Context, config *models.MongoDBConfig) error {
	col := s.db.Collection(database.ColServerConfig)
	_, err := col.UpdateOne(ctx,
		bson.M{"key": "mongodb"},
		bson.M{"$set": bson.M{"key": "mongodb", "value": config, "updated_at": time.Now()}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return err
	}

	authStr := "disabled"
	if config.AuthEnabled {
		authStr = "enabled"
	}
	journalStr := "true"
	if !config.JournalEnabled {
		journalStr = "false"
	}

	mongodConf := fmt.Sprintf(`storage:
  dbPath: /var/lib/mongodb
  journal:
    enabled: %s
  engine: %s
  wiredTiger:
    engineConfig:
      cacheSizeGB: %.1f
systemLog:
  destination: file
  logAppend: true
  path: /var/log/mongodb/mongod.log
net:
  port: 27017
  bindIp: %s
  maxIncomingConnections: %d
operationProfiling:
  slowOpThresholdMs: %d
  mode: "off"
security:
  authorization: %s
`,
		journalStr, config.StorageEngine, config.CacheSizeGB,
		config.BindIP, config.MaxConnections,
		config.SlowQueryThresholdMS, authStr,
	)

	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo '%s' > /etc/mongod.conf", mongodConf)); err != nil {
		return fmt.Errorf("failed to write mongod.conf: %w", err)
	}

	return agent.ServiceAction(ctx, "mongod", "restart")
}

// UpdateHostname changes the server's hostname.
func (s *ConfigService) UpdateHostname(ctx context.Context, hostname string) error {
	if err := agent.SetHostname(ctx, hostname); err != nil {
		return fmt.Errorf("failed to set hostname: %w", err)
	}

	// Update /etc/hosts
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i 's/127.0.1.1.*/127.0.1.1\t%s/' /etc/hosts", hostname))

	// Store in DB
	col := s.db.Collection(database.ColServerConfig)
	_, err := col.UpdateOne(ctx,
		bson.M{"key": "hostname"},
		bson.M{"$set": bson.M{"key": "hostname", "value": hostname, "updated_at": time.Now()}},
		options.Update().SetUpsert(true),
	)
	return err
}

// UpdateTimezone changes the server's timezone.
func (s *ConfigService) UpdateTimezone(ctx context.Context, timezone string) error {
	if _, err := agent.RunCommand(ctx, "timedatectl", "set-timezone", timezone); err != nil {
		return fmt.Errorf("failed to set timezone: %w", err)
	}
	col := s.db.Collection(database.ColServerConfig)
	_, err := col.UpdateOne(ctx,
		bson.M{"key": "timezone"},
		bson.M{"$set": bson.M{"key": "timezone", "value": timezone, "updated_at": time.Now()}},
		options.Update().SetUpsert(true),
	)
	return err
}

// UpdateContactEmail updates the server admin contact email.
func (s *ConfigService) UpdateContactEmail(ctx context.Context, email string) error {
	col := s.db.Collection(database.ColServerConfig)
	_, err := col.UpdateOne(ctx,
		bson.M{"key": "contact_email"},
		bson.M{"$set": bson.M{"key": "contact_email", "value": email, "updated_at": time.Now()}},
		options.Update().SetUpsert(true),
	)
	return err
}

// TestNginx validates the current Nginx configuration.
func (s *ConfigService) TestNginx(ctx context.Context) (map[string]interface{}, error) {
	output, err := agent.TestNginxConfig(ctx)
	result := map[string]interface{}{
		"output": output,
	}
	if err != nil {
		result["valid"] = false
		result["error"] = err.Error()
	} else {
		result["valid"] = true
	}
	return result, nil
}

// RestartService restarts a managed server service by name.
func (s *ConfigService) RestartService(ctx context.Context, serviceName string) error {
	allowed := map[string]bool{
		"nginx": true, "mongod": true, "postfix": true, "dovecot": true, "fail2ban": true,
	}
	if !allowed[serviceName] && !strings.HasPrefix(serviceName, "php") {
		return fmt.Errorf("service not allowed: %s", serviceName)
	}
	return agent.ServiceAction(ctx, serviceName, "restart")
}

// --- Panel access domain ---
//
// These methods let an admin point a custom domain at the WHM/cPanel web UI
// itself — e.g. change /etc/nginx/sites-available/serverpanel from
// panel.betazeninfotech.com to panel.mycompany.com, plus update
// /opt/serverpanel/.env so the Go server knows its public domain.
//
// They also (optionally) issue a Let's Encrypt certificate so access over
// HTTPS works without a manual certbot run.

var panelDomainRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$`)

// GetPanelDomain returns the current panel access domain, whether it has a
// valid Let's Encrypt cert, and the server's public IP (for DNS setup
// instructions the UI will show to the admin).
func (s *ConfigService) GetPanelDomain(ctx context.Context) (map[string]interface{}, error) {
	result := map[string]interface{}{
		"domain":     "",
		"ssl_active": false,
		"server_ip":  "",
	}

	// Current domain lives in .env as DOMAIN=
	if out, err := agent.RunCommand(ctx, "bash", "-c",
		`grep -E '^DOMAIN=' /opt/serverpanel/.env 2>/dev/null | head -1 | cut -d= -f2-`); err == nil {
		result["domain"] = strings.TrimSpace(out.Output)
	}

	// Server's primary public IPv4 — hostname -I returns space-separated IPs
	if out, err := agent.RunCommand(ctx, "bash", "-c",
		`hostname -I 2>/dev/null | awk '{print $1}'`); err == nil {
		result["server_ip"] = strings.TrimSpace(out.Output)
	}

	// SSL status: the cert exists at the standard Let's Encrypt path
	if domain, ok := result["domain"].(string); ok && domain != "" {
		certPath := fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", domain)
		if _, err := agent.RunCommand(ctx, "test", "-f", certPath); err == nil {
			result["ssl_active"] = true
		}
	}
	return result, nil
}

// ReconcilePanelDomain is the self-healing path we call on service
// startup. It catches the case where the admin previously ran
// "Update Domain" on an older binary that issued a Let's Encrypt cert
// but didn't write the :443 vhost (buildPanelVhostSSL). Symptom was
// an accessible HTTP panel plus an LE cert on disk, but
// https://<panel-domain> either served another vhost's cert
// (CERT_COMMON_NAME_INVALID) or refused connect. From that state the
// admin can't even reach the UI to click Update Domain again,
// because HSTS is often pinned by the browser after any prior visit.
//
// Reconcile is idempotent and cheap:
//   - Read DOMAIN from /opt/serverpanel/.env (source of truth).
//   - Bail out if no domain set or if the LE cert isn't on disk —
//     there's nothing we can do without a cert to bind.
//   - Read the current /etc/nginx/sites-enabled/serverpanel vhost.
//     If it already contains a "listen 443" stanza, leave it alone —
//     the admin (or a freshly installed panel) has the SSL block and
//     we don't want to fight any custom edits.
//   - Otherwise rewrite the vhost with buildPanelVhostSSL(domain, ip)
//     and reload nginx. Errors are logged and swallowed — a failed
//     reconcile must not stop the server from coming up.
//
// Runs once per process start from main.go after handler wiring.
func (s *ConfigService) ReconcilePanelDomain(ctx context.Context) {
	out, err := agent.RunCommand(ctx, "bash", "-c",
		`grep -E '^DOMAIN=' /opt/serverpanel/.env 2>/dev/null | head -1 | cut -d= -f2-`)
	if err != nil {
		return
	}
	domain := strings.TrimSpace(out.Output)
	if domain == "" || !panelDomainRe.MatchString(domain) {
		return
	}
	if !agent.LetsEncryptCertExists(domain) {
		return
	}

	vhostPath := "/etc/nginx/sites-enabled/serverpanel"
	existing, err := agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("cat %s 2>/dev/null", shellQuote(vhostPath)))
	if err == nil && strings.Contains(existing.Output, "listen 443") {
		return
	}

	ip := ""
	if ipOut, err := agent.RunCommand(ctx, "bash", "-c",
		`hostname -I 2>/dev/null | awk '{print $1}'`); err == nil {
		ip = strings.TrimSpace(ipOut.Output)
	}

	availPath := "/etc/nginx/sites-available/serverpanel"
	enabledPath := "/etc/nginx/sites-enabled/serverpanel"
	sslVhost := buildPanelVhostSSL(domain, ip)
	heredoc := fmt.Sprintf("cat > %s << 'NGINX_EOF'\n%s\nNGINX_EOF", shellQuote(availPath), sslVhost)
	if _, werr := agent.RunCommand(ctx, "bash", "-c", heredoc); werr != nil {
		return
	}
	agent.RunCommand(ctx, "ln", "-sf", availPath, enabledPath)
	_ = agent.ReloadNginx(ctx)
}

// UpdatePanelDomain points a new domain at the panel UI. The flow:
//
//  1. Validate the domain format.
//  2. Confirm DNS points at this server (best-effort A-record check).
//  3. Rewrite /etc/nginx/sites-available/serverpanel with the new server_name.
//  4. Reload nginx so the HTTP vhost responds on the new domain.
//  5. If issueSSL is true, run certbot --webroot to obtain a cert
//     (served out of /var/www/certbot, exposed by the panel vhost's
//     /.well-known/acme-challenge/ location).
//  6. Update /opt/serverpanel/.env DOMAIN= so the backend knows its
//     canonical URL (used in email links, SSO tokens, etc.).
//
// Returns the resulting state so the UI can show success/failure per step.
func (s *ConfigService) UpdatePanelDomain(ctx context.Context, domain string, issueSSL bool, email string) (map[string]interface{}, error) {
	domain = strings.TrimSpace(strings.ToLower(domain))
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if !panelDomainRe.MatchString(domain) {
		return nil, fmt.Errorf("invalid domain format: %s", domain)
	}

	// Detect the server's IP so we can (a) show it in the response,
	// (b) warn the admin if DNS resolves elsewhere, and (c) bake it
	// into the panel vhost's server_name list so requests by IP still
	// reach the panel.
	serverIP := ""
	if out, err := agent.RunCommand(ctx, "bash", "-c",
		`hostname -I 2>/dev/null | awk '{print $1}'`); err == nil {
		serverIP = strings.TrimSpace(out.Output)
	}

	// Best-effort DNS check: look up the A record and warn (don't fail) if
	// it doesn't match the server IP yet. Propagation can take minutes and
	// we don't want to block the admin from configuring nginx early.
	dnsMatches := false
	dnsResolvedTo := ""
	if out, err := agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf(`dig +short A %s @8.8.8.8 2>/dev/null | head -1`, shellQuote(domain))); err == nil {
		dnsResolvedTo = strings.TrimSpace(out.Output)
		if dnsResolvedTo != "" && dnsResolvedTo == serverIP {
			dnsMatches = true
		}
	}

	// Write the new nginx vhost. We write to a fixed filename
	// (serverpanel) so there's only ever one panel vhost, regardless of
	// how many times the domain is changed.
	vhost := buildPanelVhost(domain, serverIP)
	availPath := "/etc/nginx/sites-available/serverpanel"
	enabledPath := "/etc/nginx/sites-enabled/serverpanel"
	heredoc := fmt.Sprintf("cat > %s << 'NGINX_EOF'\n%s\nNGINX_EOF", shellQuote(availPath), vhost)
	if _, err := agent.RunCommand(ctx, "bash", "-c", heredoc); err != nil {
		return nil, fmt.Errorf("failed to write nginx vhost: %w", err)
	}
	// Make sure the site is enabled
	agent.RunCommand(ctx, "ln", "-sf", availPath, enabledPath)

	if err := agent.ReloadNginx(ctx); err != nil {
		return nil, fmt.Errorf("nginx reload failed: %w", err)
	}

	// Persist the domain in .env so the backend picks it up on restart
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		`sed -i 's|^DOMAIN=.*|DOMAIN=%s|' /opt/serverpanel/.env || echo 'DOMAIN=%s' >> /opt/serverpanel/.env`,
		domain, domain))

	// Mirror into DB so the UI's GET /config returns the latest value
	// immediately without re-reading .env.
	s.db.Collection(database.ColServerConfig).UpdateOne(ctx,
		bson.M{"key": "panel_domain"},
		bson.M{"$set": bson.M{"key": "panel_domain", "value": domain, "updated_at": time.Now()}},
		options.Update().SetUpsert(true),
	)

	result := map[string]interface{}{
		"domain":          domain,
		"server_ip":       serverIP,
		"dns_matches":     dnsMatches,
		"dns_resolved_to": dnsResolvedTo,
		"ssl_active":      false,
	}

	if issueSSL {
		if email == "" {
			email = "admin@" + domain
		}
		sslErr := agent.IssueLetsEncrypt(ctx, domain, email, nil, false)
		if sslErr == nil {
			// certbot --webroot doesn't rewrite the vhost the way the
			// legacy --nginx plugin did, so we write the SSL variant
			// ourselves: :80 serves the ACME webroot + redirects the
			// panel host to https, :443 proxies into the backend.
			sslVhost := buildPanelVhostSSL(domain, serverIP)
			heredocSSL := fmt.Sprintf("cat > %s << 'NGINX_EOF'\n%s\nNGINX_EOF", shellQuote(availPath), sslVhost)
			if _, werr := agent.RunCommand(ctx, "bash", "-c", heredocSSL); werr != nil {
				result["ssl_error"] = fmt.Sprintf("cert issued but vhost rewrite failed: %s", werr)
			} else if rerr := agent.ReloadNginx(ctx); rerr != nil {
				result["ssl_error"] = fmt.Sprintf("cert issued but nginx reload failed: %s", rerr)
			} else {
				result["ssl_active"] = true
			}
		} else {
			result["ssl_error"] = sslErr.Error()
			// Don't fail the whole request — HTTP still works, admin can
			// retry SSL once DNS propagates.
		}
	}

	return result, nil
}

// shellQuote wraps s in POSIX single-quotes, escaping any embedded single
// quote with the standard '\'' sequence.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// buildPanelVhost produces the /etc/nginx/sites-available/serverpanel
// content for a given domain. Keeps webmail + websocket + main proxy,
// identical shape to the install.sh template. Marked default_server so
// requests by raw IP (or any Host that doesn't match a vendor vhost)
// land on the panel instead of a random vendor's public_html.
func buildPanelVhost(domain, serverIP string) string {
	names := domain
	if serverIP != "" && serverIP != domain {
		names = domain + " " + serverIP
	}
	names += " _"
	return fmt.Sprintf(`server {
    listen 80 default_server;
    server_name %s;
    client_max_body_size 500M;
    client_body_timeout 600s;
    client_header_timeout 60s;
    send_timeout 600s;

    # Let's Encrypt HTTP-01 challenge — certbot --webroot writes files
    # here; LE fetches them before the rest of the panel vhost runs.
    location ^~ /.well-known/acme-challenge/ {
        root /var/www/certbot;
        default_type "text/plain";
        try_files $uri =404;
    }

    # Roundcube Webmail
    location ^~ /webmail/ {
        alias /var/lib/roundcube/public_html/;
        index index.php;

        location ~ ^/webmail/(.+\.php)$ {
            alias /var/lib/roundcube/public_html/$1;
            include fastcgi_params;
            fastcgi_pass unix:/var/run/php/php8.2-fpm.sock;
            fastcgi_param SCRIPT_FILENAME /var/lib/roundcube/public_html/$1;
            fastcgi_intercept_errors on;
        }

        location ~ /\. { deny all; }
    }

    # WebSocket support
    location /ws/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_read_timeout 3600s;
    }

    # Main panel
    location / {
        proxy_pass http://127.0.0.1:8080;
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
`, names)
}

// buildPanelVhostSSL mirrors buildPanelVhost but with HTTPS. :80 keeps
// the ACME webroot location (so renewals still work) and redirects
// the panel hostname to :443; everything else on :80 gets a 404 so a
// vendor's HTTP traffic is never forwarded through the panel. :443
// holds the full panel (webmail, ws, phpmyadmin, main reverse proxy).
// Both server blocks are default_server + catch-all server_name so
// raw-IP / unknown-host requests still hit the panel rather than
// whichever vendor vhost sorts first alphabetically.
func buildPanelVhostSSL(domain, serverIP string) string {
	names := domain
	if serverIP != "" && serverIP != domain {
		names = domain + " " + serverIP
	}
	names += " _"
	return fmt.Sprintf(`server {
    listen 80 default_server;
    server_name %s;

    location ^~ /.well-known/acme-challenge/ {
        root /var/www/certbot;
        default_type "text/plain";
        try_files $uri =404;
    }

    if ($host = "%s") { return 301 https://%s$request_uri; }
    return 404;
}

server {
    listen 443 ssl default_server;
    server_name %s;
    client_max_body_size 500M;
    client_body_timeout 600s;
    client_header_timeout 60s;
    send_timeout 600s;

    ssl_certificate /etc/letsencrypt/live/%s/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/%s/privkey.pem;

    # Roundcube Webmail
    location ^~ /webmail/ {
        alias /var/lib/roundcube/public_html/;
        index index.php;

        location ~ ^/webmail/(.+\.php)$ {
            alias /var/lib/roundcube/public_html/$1;
            include fastcgi_params;
            fastcgi_pass unix:/var/run/php/php8.2-fpm.sock;
            fastcgi_param SCRIPT_FILENAME /var/lib/roundcube/public_html/$1;
            fastcgi_intercept_errors on;
        }

        location ~ /\. { deny all; }
    }

    # phpMyAdmin (same snippet as HTTP variant — served under panel TLS)
    include /etc/nginx/snippets/phpmyadmin.conf;

    # WebSocket support
    location /ws/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_read_timeout 3600s;
    }

    # Main panel
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_request_buffering off;
        proxy_connect_timeout 60s;
        proxy_send_timeout 600s;
        proxy_read_timeout 86400;
    }
}
`, names, domain, domain, names, domain, domain)
}

func boolToOnOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// -------------------------------------------------------------------
// MySQL / MariaDB — WHM "Edit Database Configuration"
// -------------------------------------------------------------------

// MySQLConfig mirrors the knobs on WHM's Edit Database Configuration
// page. Numeric values are bytes / counts / seconds — the frontend
// formats them for display.
type MySQLConfig struct {
	MaxAllowedPacket     int64  `json:"max_allowed_packet"       bson:"max_allowed_packet"`
	MaxConnectErrors     int64  `json:"max_connect_errors"       bson:"max_connect_errors"`
	MaxConnections       int64  `json:"max_connections"          bson:"max_connections"`
	OpenFilesLimit       int64  `json:"open_files_limit"         bson:"open_files_limit"`
	PerformanceSchema    bool   `json:"performance_schema"       bson:"performance_schema"`
	SQLMode              string `json:"sql_mode"                 bson:"sql_mode"`
	ThreadCacheSize      int64  `json:"thread_cache_size"        bson:"thread_cache_size"`
	InteractiveTimeout   int64  `json:"interactive_timeout"      bson:"interactive_timeout"`
	WaitTimeout          int64  `json:"wait_timeout"             bson:"wait_timeout"`
	LogOutput            string `json:"log_output"               bson:"log_output"`             // FILE|TABLE|NONE
	LogError             string `json:"log_error"                bson:"log_error"`
	LogWarnings          int64  `json:"log_warnings"             bson:"log_warnings"`
	GeneralLog           bool   `json:"general_log"              bson:"general_log"`
	GeneralLogFile       string `json:"general_log_file"         bson:"general_log_file"`
	SlowQueryLog         bool   `json:"slow_query_log"           bson:"slow_query_log"`
	SlowQueryLogFile     string `json:"slow_query_log_file"      bson:"slow_query_log_file"`
	LongQueryTime        int64  `json:"long_query_time"          bson:"long_query_time"`
	JoinBufferSize       int64  `json:"join_buffer_size"         bson:"join_buffer_size"`
	KeyBufferSize        int64  `json:"key_buffer_size"          bson:"key_buffer_size"`
	ReadBufferSize       int64  `json:"read_buffer_size"         bson:"read_buffer_size"`
	ReadRndBufferSize    int64  `json:"read_rnd_buffer_size"     bson:"read_rnd_buffer_size"`
	SortBufferSize       int64  `json:"sort_buffer_size"         bson:"sort_buffer_size"`
	InnodbLogBufferSize  int64  `json:"innodb_log_buffer_size"   bson:"innodb_log_buffer_size"`
	InnodbLogFileSize    int64  `json:"innodb_log_file_size"     bson:"innodb_log_file_size"`
	InnodbSortBufferSize int64  `json:"innodb_sort_buffer_size"  bson:"innodb_sort_buffer_size"`
	InnodbBufferPoolSize int64  `json:"innodb_buffer_pool_size"  bson:"innodb_buffer_pool_size"`
	MaxHeapTableSize     int64  `json:"max_heap_table_size"      bson:"max_heap_table_size"`
	TmpTableSize         int64  `json:"tmp_table_size"           bson:"tmp_table_size"`
	QueryCacheSize       int64  `json:"query_cache_size"         bson:"query_cache_size"`
	QueryCacheType       int64  `json:"query_cache_type"         bson:"query_cache_type"`
}

// GetMySQLConfig parses the live values via `mysqld --verbose --help`
// (for defaults we haven't overridden) PLUS whatever the operator has
// saved in Mongo. Current values win; fallback to a sensible default.
func (s *ConfigService) GetMySQLConfig(ctx context.Context) (*MySQLConfig, error) {
	cfg := &MySQLConfig{
		MaxAllowedPacket: 268435456, MaxConnectErrors: 100, MaxConnections: 151,
		OpenFilesLimit: 40000, PerformanceSchema: false,
		SQLMode:         "STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION",
		ThreadCacheSize: 256, InteractiveTimeout: 28800, WaitTimeout: 28800,
		LogOutput: "FILE", LogError: "/var/log/mysql/error.log", LogWarnings: 2,
		GeneralLog: false, SlowQueryLog: false, LongQueryTime: 10,
		JoinBufferSize: 262144, KeyBufferSize: 134217728,
		ReadBufferSize: 131072, ReadRndBufferSize: 262144, SortBufferSize: 262144,
		InnodbLogBufferSize: 16777216, InnodbLogFileSize: 50331648,
		InnodbSortBufferSize: 1048576, InnodbBufferPoolSize: 134217728,
		MaxHeapTableSize: 16777216, TmpTableSize: 16777216,
		QueryCacheSize: 1048576, QueryCacheType: 0,
	}

	// Ask mysqld what it thinks the live values are.
	probe := "mysql -N -B -e 'SHOW GLOBAL VARIABLES' 2>/dev/null"
	if r, err := agent.RunCommand(ctx, "bash", "-c", probe); err == nil {
		m := map[string]string{}
		for _, line := range strings.Split(r.Output, "\n") {
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) == 2 {
				m[parts[0]] = strings.TrimSpace(parts[1])
			}
		}
		if v, ok := m["max_allowed_packet"]; ok { cfg.MaxAllowedPacket = parseInt64(v) }
		if v, ok := m["max_connect_errors"]; ok { cfg.MaxConnectErrors = parseInt64(v) }
		if v, ok := m["max_connections"]; ok { cfg.MaxConnections = parseInt64(v) }
		if v, ok := m["open_files_limit"]; ok { cfg.OpenFilesLimit = parseInt64(v) }
		if v, ok := m["performance_schema"]; ok { cfg.PerformanceSchema = v == "ON" }
		if v, ok := m["sql_mode"]; ok { cfg.SQLMode = v }
		if v, ok := m["thread_cache_size"]; ok { cfg.ThreadCacheSize = parseInt64(v) }
		if v, ok := m["interactive_timeout"]; ok { cfg.InteractiveTimeout = parseInt64(v) }
		if v, ok := m["wait_timeout"]; ok { cfg.WaitTimeout = parseInt64(v) }
		if v, ok := m["log_output"]; ok { cfg.LogOutput = v }
		if v, ok := m["log_error"]; ok { cfg.LogError = v }
		if v, ok := m["log_warnings"]; ok { cfg.LogWarnings = parseInt64(v) }
		if v, ok := m["general_log"]; ok { cfg.GeneralLog = v == "ON" }
		if v, ok := m["general_log_file"]; ok { cfg.GeneralLogFile = v }
		if v, ok := m["slow_query_log"]; ok { cfg.SlowQueryLog = v == "ON" }
		if v, ok := m["slow_query_log_file"]; ok { cfg.SlowQueryLogFile = v }
		if v, ok := m["long_query_time"]; ok { cfg.LongQueryTime = int64(parseFloat(v)) }
		if v, ok := m["join_buffer_size"]; ok { cfg.JoinBufferSize = parseInt64(v) }
		if v, ok := m["key_buffer_size"]; ok { cfg.KeyBufferSize = parseInt64(v) }
		if v, ok := m["read_buffer_size"]; ok { cfg.ReadBufferSize = parseInt64(v) }
		if v, ok := m["read_rnd_buffer_size"]; ok { cfg.ReadRndBufferSize = parseInt64(v) }
		if v, ok := m["sort_buffer_size"]; ok { cfg.SortBufferSize = parseInt64(v) }
		if v, ok := m["innodb_log_buffer_size"]; ok { cfg.InnodbLogBufferSize = parseInt64(v) }
		if v, ok := m["innodb_log_file_size"]; ok { cfg.InnodbLogFileSize = parseInt64(v) }
		if v, ok := m["innodb_sort_buffer_size"]; ok { cfg.InnodbSortBufferSize = parseInt64(v) }
		if v, ok := m["innodb_buffer_pool_size"]; ok { cfg.InnodbBufferPoolSize = parseInt64(v) }
		if v, ok := m["max_heap_table_size"]; ok { cfg.MaxHeapTableSize = parseInt64(v) }
		if v, ok := m["tmp_table_size"]; ok { cfg.TmpTableSize = parseInt64(v) }
		if v, ok := m["query_cache_size"]; ok { cfg.QueryCacheSize = parseInt64(v) }
		if v, ok := m["query_cache_type"]; ok {
			switch v {
			case "OFF", "0": cfg.QueryCacheType = 0
			case "ON", "1":  cfg.QueryCacheType = 1
			case "DEMAND", "2": cfg.QueryCacheType = 2
			}
		}
	}
	return cfg, nil
}

// UpdateMySQLConfig writes a cpanel-owned drop-in /etc/mysql/conf.d/99-panel.cnf
// and restarts mariadb. The drop-in approach means we never touch the distro
// my.cnf (easy to roll back, survives package upgrades).
func (s *ConfigService) UpdateMySQLConfig(ctx context.Context, cfg *MySQLConfig) error {
	col := s.db.Collection(database.ColServerConfig)
	_, _ = col.UpdateOne(ctx,
		bson.M{"key": "mysql"},
		bson.M{"$set": bson.M{"key": "mysql", "value": cfg, "updated_at": time.Now()}},
		options.Update().SetUpsert(true),
	)

	perf := "OFF"; if cfg.PerformanceSchema { perf = "ON" }
	genLog := "0"; if cfg.GeneralLog { genLog = "1" }
	slowLog := "0"; if cfg.SlowQueryLog { slowLog = "1" }

	conf := fmt.Sprintf(`# Managed by Betazen Server Panel — edits here are rewritten.
[mysqld]
max_allowed_packet      = %d
max_connect_errors      = %d
max_connections         = %d
open_files_limit        = %d
performance_schema      = %s
sql_mode                = %q
thread_cache_size       = %d
interactive_timeout     = %d
wait_timeout            = %d

log_output              = %s
log_error               = %s
log_warnings            = %d
general_log             = %s
general_log_file        = %s
slow_query_log          = %s
slow_query_log_file     = %s
long_query_time         = %d

join_buffer_size        = %d
key_buffer_size         = %d
read_buffer_size        = %d
read_rnd_buffer_size    = %d
sort_buffer_size        = %d

innodb_log_buffer_size  = %d
innodb_log_file_size    = %d
innodb_sort_buffer_size = %d
innodb_buffer_pool_size = %d

max_heap_table_size     = %d
tmp_table_size          = %d

query_cache_size        = %d
query_cache_type        = %d
`,
		cfg.MaxAllowedPacket, cfg.MaxConnectErrors, cfg.MaxConnections,
		cfg.OpenFilesLimit, perf, cfg.SQLMode, cfg.ThreadCacheSize,
		cfg.InteractiveTimeout, cfg.WaitTimeout,
		cfg.LogOutput, cfg.LogError, cfg.LogWarnings,
		genLog, cfg.GeneralLogFile, slowLog, cfg.SlowQueryLogFile, cfg.LongQueryTime,
		cfg.JoinBufferSize, cfg.KeyBufferSize, cfg.ReadBufferSize,
		cfg.ReadRndBufferSize, cfg.SortBufferSize,
		cfg.InnodbLogBufferSize, cfg.InnodbLogFileSize,
		cfg.InnodbSortBufferSize, cfg.InnodbBufferPoolSize,
		cfg.MaxHeapTableSize, cfg.TmpTableSize,
		cfg.QueryCacheSize, cfg.QueryCacheType,
	)

	// Pick the directory that exists on this distro. Debian/Ubuntu use
	// /etc/mysql/mariadb.conf.d, RHEL/CloudLinux use /etc/my.cnf.d.
	dir := "/etc/mysql/mariadb.conf.d"
	if r, err := agent.RunCommand(ctx, "bash", "-c", "test -d /etc/my.cnf.d && echo yes"); err == nil && strings.TrimSpace(r.Output) == "yes" {
		dir = "/etc/my.cnf.d"
	}
	path := dir + "/99-panel.cnf"
	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("cat > %s <<'EOF'\n%sEOF", path, conf)); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	// Restart mariadb; fall back to mysql. Ignore errors from the
	// unit that doesn't exist on this box.
	if err := agent.ServiceAction(ctx, "mariadb", "restart"); err != nil {
		_ = agent.ServiceAction(ctx, "mysql", "restart")
	}
	return nil
}

// RepairDatabase runs `mysqlcheck --auto-repair --optimize` on the given
// MySQL/MariaDB database. Returns the raw mysqlcheck output so the UI
// can show the operator which tables needed repair.
func (s *ConfigService) RepairDatabase(ctx context.Context, dbName string) (string, error) {
	if dbName == "" || strings.ContainsAny(dbName, " ;|&`$()<>\"'\\") {
		return "", fmt.Errorf("invalid database name")
	}
	r, err := agent.RunCommand(ctx, "mysqlcheck", "--auto-repair", "--optimize", "--databases", dbName)
	if err != nil {
		return r.Output, fmt.Errorf("mysqlcheck: %w", err)
	}
	return r.Output, nil
}

// ListMySQLDatabases returns every user database (skipping internal
// schemas) so the UI can populate the Repair Databases picker.
func (s *ConfigService) ListMySQLDatabases(ctx context.Context) ([]string, error) {
	r, err := agent.RunCommand(ctx, "bash", "-c",
		"mysql -N -B -e 'SHOW DATABASES' 2>/dev/null | grep -Ev '^(information_schema|mysql|performance_schema|sys|phpmyadmin)$' | sort")
	if err != nil {
		return []string{}, nil // empty on error rather than 500
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(r.Output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

// -------------------------------------------------------------------
// MultiPHP INI Editor — per-version php.ini
// -------------------------------------------------------------------

// PHPIniDirective is one editable key/value row in the MultiPHP INI
// editor. Info is a short human-readable hint for the tooltip.
type PHPIniDirective struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Info  string `json:"info"`
}

// BasicPHPDirectives are the directives surfaced in "Basic Mode". Order
// matches WHM's UI so operators who switch between the two panels don't
// have to hunt.
var BasicPHPDirectives = []struct{ Key, Info string }{
	{"allow_url_fopen", "Allow fopen() to open URLs"},
	{"allow_url_include", "Allow include() of URLs (security-sensitive)"},
	{"display_errors", "Print errors to the response body"},
	{"enable_dl", "Allow dl() dynamic extension loading"},
	{"file_uploads", "Allow uploaded files via POST"},
	{"log_errors", "Write errors to error_log"},
	{"max_execution_time", "Max seconds a script may run"},
	{"max_input_time", "Max seconds parsing request data"},
	{"max_input_vars", "Max GET/POST/COOKIE variables"},
	{"memory_limit", "Max memory per script"},
	{"post_max_size", "Largest POST body accepted"},
	{"register_argc_argv", "Populate $argc/$argv from GET"},
	{"session.gc_maxlifetime", "Seconds before a session is garbage-collected"},
	{"session.save_path", "Where session files live"},
	{"short_open_tag", "Allow <? as well as <?php"},
	{"upload_max_filesize", "Largest uploaded file accepted"},
	{"zlib.output_compression", "gzip compress responses"},
}

// ListPHPVersions discovers every PHP CLI binary on PATH (php, phpX,
// phpX.Y). Used by the MultiPHP INI Editor to populate the version
// picker.
func (s *ConfigService) ListPHPVersions(ctx context.Context) ([]string, error) {
	r, err := agent.RunCommand(ctx, "bash", "-c",
		"ls /etc/php/ 2>/dev/null; ls -d /opt/cpanel/ea-php* 2>/dev/null | xargs -n1 basename 2>/dev/null")
	if err != nil {
		return []string{}, nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(r.Output), "\n") {
		v := strings.TrimSpace(line)
		if v == "" { continue }
		v = strings.TrimPrefix(v, "ea-php")
		if _, ok := seen[v]; ok { continue }
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out, nil
}

// phpIniPath resolves the default php.ini for a given version.
func phpIniPath(ctx context.Context, version string) string {
	for _, p := range []string{
		fmt.Sprintf("/etc/php/%s/fpm/php.ini", version),
		fmt.Sprintf("/etc/php/%s/cli/php.ini", version),
		fmt.Sprintf("/opt/cpanel/ea-php%s/root/etc/php.ini", strings.ReplaceAll(version, ".", "")),
	} {
		if r, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("test -f %s && echo %s", p, p)); err == nil && strings.TrimSpace(r.Output) != "" {
			return strings.TrimSpace(r.Output)
		}
	}
	return ""
}

// GetPHPIniDirectives returns the basic-mode subset of php.ini for the
// requested version, filled in from the live ini file.
func (s *ConfigService) GetPHPIniDirectives(ctx context.Context, version string) ([]PHPIniDirective, error) {
	path := phpIniPath(ctx, version)
	current := map[string]string{}
	if path != "" {
		if r, err := agent.RunCommand(ctx, "cat", path); err == nil {
			for _, line := range strings.Split(r.Output, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, ";") {
					continue
				}
				kv := strings.SplitN(line, "=", 2)
				if len(kv) != 2 { continue }
				k := strings.TrimSpace(kv[0])
				v := strings.TrimSpace(kv[1])
				v = strings.Trim(v, "\"")
				current[k] = v
			}
		}
	}
	out := make([]PHPIniDirective, 0, len(BasicPHPDirectives))
	for _, d := range BasicPHPDirectives {
		out = append(out, PHPIniDirective{Key: d.Key, Value: current[d.Key], Info: d.Info})
	}
	return out, nil
}

// UpdatePHPIniDirectives rewrites each key via `sed`. Keeps comments
// intact; if the key is missing it's appended. Reloads PHP-FPM on
// success (matching the existing UpdatePHP behaviour).
func (s *ConfigService) UpdatePHPIniDirectives(ctx context.Context, version string, dirs []PHPIniDirective) error {
	path := phpIniPath(ctx, version)
	if path == "" {
		return fmt.Errorf("php.ini for PHP %s not found", version)
	}
	for _, d := range dirs {
		if d.Key == "" { continue }
		// sed: replace `^;?\s*<key>\s*=.*` → `<key> = <value>`, else append.
		keyRe := regexp.MustCompile(`^[A-Za-z0-9_.]+$`)
		if !keyRe.MatchString(d.Key) {
			continue
		}
		// Escape value for sed: we round-trip through bash -c so the
		// safest thing is to write each directive on its own line via
		// printf, then grep+sed to patch it in.
		script := fmt.Sprintf(
			`F=%q; K=%q; V=%q;`+
				` if grep -qE "^;?[[:space:]]*${K}[[:space:]]*=" "$F";`+
				` then sed -i "s|^;\?[[:space:]]*${K}[[:space:]]*=.*|${K} = ${V}|" "$F";`+
				` else echo "${K} = ${V}" >> "$F"; fi`,
			path, d.Key, d.Value,
		)
		if _, err := agent.RunCommand(ctx, "bash", "-c", script); err != nil {
			return fmt.Errorf("patching %s: %w", d.Key, err)
		}
	}
	// Reload the matching php-fpm unit if any.
	_ = agent.ServiceAction(ctx, fmt.Sprintf("php%s-fpm", version), "reload")
	return nil
}

// -------------------------------------------------------------------
// Server reboot — WHM "Forceful Server Reboot" + "Graceful Server Reboot"
// -------------------------------------------------------------------

// GracefulReboot sends SIGTERM to running services and schedules a
// reboot in 1 minute so the panel has a chance to return a response
// first. Operator can cancel with `shutdown -c`.
func (s *ConfigService) GracefulReboot(ctx context.Context) error {
	_, err := agent.RunCommand(ctx, "bash", "-c", "shutdown -r +1 'Scheduled reboot from Betazen Server Panel'")
	return err
}

// ForcefulReboot skips the SIGTERM/systemd-shutdown dance. Used when
// a graceful reboot hangs. Still delays a minute to let the panel
// reply first — but passes --force so systemd doesn't try to stop
// units in the normal order.
func (s *ConfigService) ForcefulReboot(ctx context.Context) error {
	_, err := agent.RunCommand(ctx, "bash", "-c", "(sleep 2 && systemctl --force reboot) & disown")
	return err
}

// -------------------------------------------------------------------
// Small parse helpers used by Mongo-saved value hydration above.
// -------------------------------------------------------------------

func parseInt64(s string) int64 {
	var n int64
	_, _ = fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
	return n
}
func parseFloat(s string) float64 {
	var f float64
	_, _ = fmt.Sscanf(strings.TrimSpace(s), "%f", &f)
	return f
}
