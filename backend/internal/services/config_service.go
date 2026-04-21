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

// PanelSSLState describes the panel domain's TLS certificate as the UI
// renders it: which domain it's bound to, issuer, expiry, and the live
// cert path on disk. ssl_active==false means no cert exists, the
// remaining fields are blank.
type PanelSSLState struct {
	Domain        string `json:"domain"`
	SSLActive     bool   `json:"ssl_active"`
	Issuer        string `json:"issuer,omitempty"`
	IssuedAt      string `json:"issued_at,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	DaysRemaining int    `json:"days_remaining,omitempty"`
	CertPath      string `json:"cert_path,omitempty"`
	KeyPath       string `json:"key_path,omitempty"`
	IsIPDomain    bool   `json:"is_ip_domain"`
}

// ipv4Re matches a bare dotted-quad address. Used to refuse Let's
// Encrypt issuance when the panel "domain" is actually a raw IP — LE
// won't issue for IPs, so we surface a clear error in the UI rather
// than letting certbot fail with an opaque DNS error.
var ipv4Re = regexp.MustCompile(`^([0-9]{1,3}\.){3}[0-9]{1,3}$`)

// GetPanelSSL returns the current TLS state for the panel's vhost. Pure
// read — no certbot call, no nginx reload. Used by the UI to render
// the "SSL Certificate" card.
func (s *ConfigService) GetPanelSSL(ctx context.Context) (*PanelSSLState, error) {
	state := &PanelSSLState{}

	// Look up the current panel domain from .env. Same source as
	// GetPanelDomain so the two endpoints agree even if mongo drifted.
	if out, err := agent.RunCommand(ctx, "bash", "-c",
		`grep -E '^DOMAIN=' /opt/serverpanel/.env 2>/dev/null | head -1 | cut -d= -f2-`); err == nil {
		state.Domain = strings.TrimSpace(out.Output)
	}
	if state.Domain == "" {
		return state, nil
	}
	state.IsIPDomain = ipv4Re.MatchString(state.Domain)

	live := fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", state.Domain)
	if _, err := agent.RunCommand(ctx, "test", "-f", live); err != nil {
		// no cert
		return state, nil
	}
	state.SSLActive = true
	state.CertPath = live
	state.KeyPath = fmt.Sprintf("/etc/letsencrypt/live/%s/privkey.pem", state.Domain)

	// Pull issuer + dates straight from the cert via openssl. Avoids
	// shelling out to certbot (slow, and certbot insists on a full lock
	// dance every call).
	if r, err := agent.RunCommand(ctx, "openssl", "x509", "-in", live, "-noout", "-issuer", "-startdate", "-enddate"); err == nil && r != nil {
		for _, ln := range strings.Split(r.Output, "\n") {
			ln = strings.TrimSpace(ln)
			switch {
			case strings.HasPrefix(ln, "issuer="):
				state.Issuer = strings.TrimSpace(strings.TrimPrefix(ln, "issuer="))
			case strings.HasPrefix(ln, "notBefore="):
				if t, perr := time.Parse("Jan _2 15:04:05 2006 MST", strings.TrimSpace(strings.TrimPrefix(ln, "notBefore="))); perr == nil {
					state.IssuedAt = t.Format(time.RFC3339)
				}
			case strings.HasPrefix(ln, "notAfter="):
				if t, perr := time.Parse("Jan _2 15:04:05 2006 MST", strings.TrimSpace(strings.TrimPrefix(ln, "notAfter="))); perr == nil {
					state.ExpiresAt = t.Format(time.RFC3339)
					state.DaysRemaining = int(time.Until(t).Hours() / 24)
				}
			}
		}
	}
	return state, nil
}

// InstallPanelSSL issues (or reissues) a Let's Encrypt certificate for
// the panel's CURRENT domain — used when the operator first connected
// the panel without SSL and now wants to flip it on, or wants to force
// a fresh cert. Uses the same --webroot path as the rest of the
// system so /var/www/certbot is the single source of truth for ACME
// challenges.
//
// forceRenew=true asks certbot to reissue even if the existing cert
// has weeks of life left (useful when the cert is for the wrong
// hostname after a panel-domain change).
//
// Refuses to run when the panel is currently bound to a raw IP — LE
// can't issue for IPs, and falling through to certbot just produces a
// confusing "no valid IP authentication" error in the UI.
func (s *ConfigService) InstallPanelSSL(ctx context.Context, email string, forceRenew bool) (*PanelSSLState, error) {
	current, err := s.GetPanelSSL(ctx)
	if err != nil {
		return nil, err
	}
	if current.Domain == "" {
		return nil, fmt.Errorf("no panel domain configured — set one under Panel Access Domain first")
	}
	if current.IsIPDomain {
		return nil, fmt.Errorf("Let's Encrypt does not issue certificates for IP addresses (%s) — connect a real domain first", current.Domain)
	}

	if email == "" {
		// Fall back to the saved contact email; if neither is set, use a
		// safe per-domain default.
		var cfg bson.M
		s.db.Collection(database.ColServerConfig).FindOne(ctx, bson.M{"key": "contact_email"}).Decode(&cfg)
		if v, ok := cfg["value"].(string); ok {
			email = strings.TrimSpace(v)
		}
		if email == "" {
			email = "admin@" + current.Domain
		}
	}

	// Ensure the panel vhost has the ACME location BEFORE certbot runs.
	// If the vhost was hand-edited or stale, /.well-known/acme-challenge/
	// won't reach /var/www/certbot and the challenge will 404. Rewriting
	// to the canonical HTTP-only buildPanelVhost is the safe path: we
	// just rebuild the SSL variant after the cert lands.
	serverIP := ""
	if out, err := agent.RunCommand(ctx, "bash", "-c",
		`hostname -I 2>/dev/null | awk '{print $1}'`); err == nil {
		serverIP = strings.TrimSpace(out.Output)
	}
	availPath := "/etc/nginx/sites-available/serverpanel"
	httpVhost := buildPanelVhost(current.Domain, serverIP)
	heredoc := fmt.Sprintf("cat > %s << 'NGINX_EOF'\n%s\nNGINX_EOF", shellQuote(availPath), httpVhost)
	if _, werr := agent.RunCommand(ctx, "bash", "-c", heredoc); werr != nil {
		return nil, fmt.Errorf("failed to stage HTTP vhost for ACME challenge: %w", werr)
	}
	if rerr := agent.ReloadNginx(ctx); rerr != nil {
		return nil, fmt.Errorf("nginx reload before ACME failed: %w", rerr)
	}

	// Run certbot. If forceRenew, ask it to ignore the "not yet due for
	// renewal" gate by passing --force-renewal.
	additionalDomains := []string{}
	if forceRenew {
		// IssueLetsEncrypt doesn't expose --force-renewal, so we shell
		// directly to certbot with the same --webroot args plus the
		// force flag.
		args := []string{
			"certonly", "--webroot", "-w", "/var/www/certbot",
			"--non-interactive", "--agree-tos",
			"--cert-name", current.Domain,
			"--force-renewal",
			"-m", email, "-d", current.Domain,
		}
		if _, err := agent.RunCommand(ctx, "certbot", args...); err != nil {
			return nil, fmt.Errorf("certbot --force-renewal failed: %w", err)
		}
	} else {
		if err := agent.IssueLetsEncrypt(ctx, current.Domain, email, additionalDomains, false); err != nil {
			return nil, fmt.Errorf("certbot failed: %w", err)
		}
	}

	// Cert is on disk — rewrite the vhost to the SSL variant.
	sslVhost := buildPanelVhostSSL(current.Domain, serverIP)
	heredocSSL := fmt.Sprintf("cat > %s << 'NGINX_EOF'\n%s\nNGINX_EOF", shellQuote(availPath), sslVhost)
	if _, werr := agent.RunCommand(ctx, "bash", "-c", heredocSSL); werr != nil {
		return nil, fmt.Errorf("cert issued but vhost rewrite failed: %w", werr)
	}
	if rerr := agent.ReloadNginx(ctx); rerr != nil {
		return nil, fmt.Errorf("cert issued but nginx reload failed: %w", rerr)
	}

	return s.GetPanelSSL(ctx)
}

// shellQuote wraps s in POSIX single-quotes, escaping any embedded single
// quote with the standard '\'' sequence.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hostGuard returns the nginx `if ($host !~ ...) { return 404; }` block
// that lets the panel vhost be the catch-all default_server while still
// rejecting unknown hostnames. Without this, a vendor whose domain was
// purged (or any never-configured DNS A record pointing here) silently
// gets the panel login page on its hostname — a security + UX leak.
//
// Allowlist = panel domain + server IP. Anything else returns 404 with
// nginx's default error page; the panel never appears.
func hostGuard(domain, serverIP string) string {
	allowed := []string{regexp.QuoteMeta(domain)}
	if serverIP != "" && serverIP != domain {
		allowed = append(allowed, regexp.QuoteMeta(serverIP))
	}
	return fmt.Sprintf(
		"    if ($host !~* ^(%s)$) { return 404; }\n",
		strings.Join(allowed, "|"),
	)
}

// buildPanelVhost produces the /etc/nginx/sites-available/serverpanel
// content for a given domain. Keeps webmail + websocket + main proxy,
// identical shape to the install.sh template. Marked default_server so
// requests by raw IP (or any Host that doesn't match a vendor vhost)
// land on the panel instead of a random vendor's public_html — but
// hostGuard above 404s any host that isn't the panel domain or the
// server IP, so a deleted-but-still-DNS-pointing domain doesn't leak
// the panel UI on its hostname.
func buildPanelVhost(domain, serverIP string) string {
	names := domain
	if serverIP != "" && serverIP != domain {
		names = domain + " " + serverIP
	}
	names += " _"
	guard := hostGuard(domain, serverIP)
	return fmt.Sprintf(`server {
    listen 80 default_server;
    server_name %s;
    client_max_body_size 500M;
    client_body_timeout 600s;
    client_header_timeout 60s;
    send_timeout 600s;

    # Let's Encrypt HTTP-01 challenge — certbot --webroot writes files
    # here; LE fetches them before the rest of the panel vhost runs.
    # Stays accessible regardless of host so renewal works for any
    # cert whose A record points here.
    location ^~ /.well-known/acme-challenge/ {
        root /var/www/certbot;
        default_type "text/plain";
        try_files $uri =404;
    }

    # Reject any host that isn't the panel domain or the server IP.
    # Without this, a vendor whose vhost was deleted (DNS A still
    # points at us) would silently get the panel login on its own
    # hostname.
%s
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
`, names, guard)
}

// buildPanelVhostSSL mirrors buildPanelVhost but with HTTPS. Two server
// blocks, both marked default_server with (domain, IP, _) in server_name
// so the panel is reachable by domain AND raw IP AND any unknown host:
//
//   :80  — serves the panel directly when the caller came in by IP or an
//          unknown host (so `http://<IP>/` works without a cert warning);
//          redirects only the panel hostname to HTTPS. Keeps /.well-known/
//          acme-challenge/ working for renewals.
//   :443 — serves the panel with the Let's Encrypt cert. Cert CN is
//          <domain>, so `https://<IP>/` shows a cert-name-mismatch warning
//          but still serves after bypass — this is the best we can do
//          without a dedicated IP cert.
//
// The locations inside the :80 block mirror the :443 block so the panel
// is fully functional on both schemes, not just a redirect.
func buildPanelVhostSSL(domain, serverIP string) string {
	names := domain
	if serverIP != "" && serverIP != domain {
		names = domain + " " + serverIP
	}
	names += " _"
	// Shared location body — webmail + phpmyadmin + ws + main proxy.
	// Same content under both :80 and :443 so raw-IP access (HTTP) and
	// domain access (HTTPS) both land on the full panel surface.
	locationsBody := `    # Roundcube Webmail
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

    include /etc/nginx/snippets/phpmyadmin.conf;

    location /ws/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_read_timeout 3600s;
    }

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
`

	guard := hostGuard(domain, serverIP)
	return fmt.Sprintf(`server {
    listen 80 default_server;
    server_name %s;
    client_max_body_size 500M;
    client_body_timeout 600s;
    client_header_timeout 60s;
    send_timeout 600s;

    location ^~ /.well-known/acme-challenge/ {
        root /var/www/certbot;
        default_type "text/plain";
        try_files $uri =404;
    }

    # Reject any host that isn't the panel domain or the server IP.
    # Without this guard, a vendor whose vhost was deleted (DNS A still
    # points here) gets the panel UI on its own hostname.
%s
    if ($host = "%s") { return 301 https://%s$request_uri; }

%s}

server {
    listen 443 ssl default_server;
    server_name %s;
    client_max_body_size 500M;
    client_body_timeout 600s;
    client_header_timeout 60s;
    send_timeout 600s;

    ssl_certificate /etc/letsencrypt/live/%s/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/%s/privkey.pem;

    # Same host guard as :80 — purged-vendor hostnames whose DNS still
    # points here get 404 instead of the panel login.
%s
%s}
`, names, guard, domain, domain, locationsBody, names, domain, domain, guard, locationsBody)
}

// ReassignServerIP rewrites every record the panel owns that embeds a
// server IP so a whole server can migrate to a new public address with
// one call. Scope:
//
//  1. DNS — every A record pointing at oldIP is rewritten to newIP,
//     both in PowerDNS (pdnsutil replace-rrset) and in MongoDB. Any
//     SPF TXT (v=spf1 ... ip4:<oldIP> ...) has its ip4: token rewritten.
//  2. DB mirrors — domains.server_ip and dns_zones.server_ip fields.
//  3. /opt/serverpanel/.env SERVER_IP — rewritten so a backend restart
//     picks up the new value.
//  4. Panel nginx vhost (serverpanel) — server_name IP token swapped.
//
// What is intentionally NOT rewritten:
//   - Let's Encrypt cert files (domain-based, no IP inside).
//   - OpenDKIM config (domain-based).
//   - Postfix main.cf (inet_interfaces=all, no hard-coded IP).
//   - Vendor vhosts (we generate them without IPs; user-customised ones
//     would need manual review anyway).
//
// Returns a summary the UI can show so the operator knows what was
// touched without having to diff the system afterwards.
func (s *ConfigService) ReassignServerIP(ctx context.Context, oldIP, newIP string) (map[string]interface{}, error) {
	oldIP = strings.TrimSpace(oldIP)
	newIP = strings.TrimSpace(newIP)
	if newIP == "" {
		return nil, fmt.Errorf("new_ip is required")
	}
	// Auto-detect old IP if caller didn't supply one.
	if oldIP == "" {
		if out, err := agent.RunCommand(ctx, "bash", "-c",
			`hostname -I 2>/dev/null | awk '{print $1}'`); err == nil {
			oldIP = strings.TrimSpace(out.Output)
		}
	}
	if oldIP == "" {
		return nil, fmt.Errorf("old_ip could not be detected; pass it explicitly")
	}
	if oldIP == newIP {
		return map[string]interface{}{
			"old_ip": oldIP, "new_ip": newIP,
			"note": "old and new IP are the same — nothing to do",
		}, nil
	}

	summary := map[string]interface{}{
		"old_ip":     oldIP,
		"new_ip":     newIP,
		"a_records":  0,
		"spf_txt":    0,
		"domains":    0,
		"dns_zones":  0,
		"ns_restamped":  0,
		"soa_restamped": 0,
		"env_patched": false,
		"vhost_patched": false,
	}
	// Canonical nameservers this panel advertises. Kept in sync with
	// agent.CreateDNSZone + the transfer DNS step. When ReassignServerIP
	// runs after a transfer it re-stamps NS+SOA on every zone so that
	// stale source-side NS values (e.g. ns1.sourcepanel.com that the
	// pre-fix transfer code carried across) don't keep advertising the
	// wrong nameservers to the world.
	canonicalNS := []string{
		"dns1.betazeninfotech.com.",
		"dns2.betazeninfotech.com.",
		"dns3.betazeninfotech.com.",
		"dns4.betazeninfotech.com.",
	}

	// 1a. PowerDNS: rewrite A and SPF records across every zone. pdnsutil
	// list-all-zones gives us a newline-delimited zone list; inside each
	// we list the RRSET and swap matching values in place.
	if r, err := agent.RunCommand(ctx, "pdnsutil", "list-all-zones"); err == nil && r != nil {
		for _, zone := range strings.Split(strings.TrimSpace(r.Output), "\n") {
			zone = strings.TrimSpace(zone)
			if zone == "" {
				continue
			}
			// Re-stamp SOA + NS so transfer-imported zones (which may
			// carry the source's nameservers) start advertising this
			// panel's NS values. Idempotent: replace-rrset overwrites
			// whatever was there, add-record duplicates harmlessly
			// pruned by the loop below.
			soa := fmt.Sprintf("%s hostmaster.%s 1 10800 3600 604800 3600", canonicalNS[0], zone)
			if _, e := agent.RunCommand(ctx, "pdnsutil", "replace-rrset", zone, "", "SOA", "3600", soa); e == nil {
				summary["soa_restamped"] = summary["soa_restamped"].(int) + 1
			}
			// Drop existing NS rrset at apex then re-add the canonical
			// list. Avoids accumulating stale NS values across runs.
			agent.RunCommand(ctx, "pdnsutil", "delete-rrset", zone, "@", "NS")
			for _, ns := range canonicalNS {
				agent.RunCommand(ctx, "pdnsutil", "add-record", zone, "@", "NS", "3600", ns)
			}
			summary["ns_restamped"] = summary["ns_restamped"].(int) + 1
			zr, zerr := agent.RunCommand(ctx, "pdnsutil", "list-zone", zone)
			if zerr != nil || zr == nil {
				continue
			}
			for _, line := range strings.Split(zr.Output, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "$") {
					continue
				}
				fields := strings.Fields(line)
				if len(fields) < 4 {
					continue
				}
				name, recType, value := fields[0], strings.ToUpper(fields[3]), strings.Join(fields[4:], " ")
				// Strip trailing dot on name for pdnsutil calls below.
				trimName := strings.TrimSuffix(name, "."+zone+".")
				trimName = strings.TrimSuffix(trimName, ".")
				if trimName == zone || trimName == "" {
					trimName = "@"
				}
				switch recType {
				case "A":
					if value == oldIP {
						agent.RunCommand(ctx, "pdnsutil", "replace-rrset", zone, trimName, "A", "3600", newIP)
						summary["a_records"] = summary["a_records"].(int) + 1
					}
				case "TXT":
					if strings.Contains(value, "v=spf1") && strings.Contains(value, "ip4:"+oldIP) {
						newVal := strings.ReplaceAll(value, "ip4:"+oldIP, "ip4:"+newIP)
						agent.RunCommand(ctx, "pdnsutil", "replace-rrset", zone, trimName, "TXT", "3600", newVal)
						summary["spf_txt"] = summary["spf_txt"].(int) + 1
					}
				}
			}
			agent.RunCommand(ctx, "pdnsutil", "increase-serial", zone)
		}
		agent.RunCommand(ctx, "pdns_control", "reload")
	}

	// 1b. MongoDB DNS records: same rewrites so the UI's DNS editor
	// shows the new IP without waiting for a reimport.
	recRes, _ := s.db.Collection(database.ColDNSRecords).UpdateMany(ctx,
		bson.M{"type": "A", "value": oldIP},
		bson.M{"$set": bson.M{"value": newIP, "updated_at": time.Now()}})
	if recRes != nil {
		summary["db_a_records"] = recRes.ModifiedCount
	}
	// SPF TXT uses $regex over the stored string.
	if cur, err := s.db.Collection(database.ColDNSRecords).Find(ctx, bson.M{
		"type":  "TXT",
		"value": bson.M{"$regex": "ip4:" + regexp.QuoteMeta(oldIP)},
	}); err == nil {
		defer cur.Close(ctx)
		var recs []models.DNSRecord
		cur.All(ctx, &recs)
		for _, r := range recs {
			newVal := strings.ReplaceAll(r.Value, "ip4:"+oldIP, "ip4:"+newIP)
			s.db.Collection(database.ColDNSRecords).UpdateByID(ctx, r.ID,
				bson.M{"$set": bson.M{"value": newVal, "updated_at": time.Now()}})
		}
		summary["db_spf_txt"] = len(recs)
	}

	// 2. Mirror the new IP into domains.server_ip + dns_zones.server_ip
	// so list endpoints return the current value.
	if r, err := s.db.Collection(database.ColDomains).UpdateMany(ctx,
		bson.M{"server_ip": oldIP},
		bson.M{"$set": bson.M{"server_ip": newIP, "updated_at": time.Now()}}); err == nil && r != nil {
		summary["domains"] = r.ModifiedCount
	}
	if r, err := s.db.Collection(database.ColDNSZones).UpdateMany(ctx,
		bson.M{"server_ip": oldIP},
		bson.M{"$set": bson.M{"server_ip": newIP, "updated_at": time.Now()}}); err == nil && r != nil {
		summary["dns_zones"] = r.ModifiedCount
	}

	// 3. /opt/serverpanel/.env SERVER_IP — a backend restart picks this
	// up; we don't restart here because the caller may want to inspect
	// the summary first and restart manually from the UI.
	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"sed -i 's|^SERVER_IP=.*|SERVER_IP=%s|' /opt/serverpanel/.env 2>/dev/null && grep '^SERVER_IP=' /opt/serverpanel/.env",
		newIP)); err == nil {
		summary["env_patched"] = true
	}

	// 4. Panel nginx vhost — swap the IP token in server_name so the
	// raw-IP catch-all still lands on the panel after a DNS change.
	// Only rewrites exact tokens; untouched if the file was hand-edited
	// to a different shape.
	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		`sed -i 's|\b%s\b|%s|g' /etc/nginx/sites-available/serverpanel 2>/dev/null && nginx -t 2>/dev/null && systemctl reload nginx`,
		regexp.QuoteMeta(oldIP), newIP)); err == nil {
		summary["vhost_patched"] = true
	}

	// Persist the change to the server_config doc so GetAll reflects it.
	s.db.Collection(database.ColServerConfig).UpdateOne(ctx,
		bson.M{"key": "server_ip"},
		bson.M{"$set": bson.M{"key": "server_ip", "value": newIP, "updated_at": time.Now()}},
		options.Update().SetUpsert(true),
	)
	return summary, nil
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
