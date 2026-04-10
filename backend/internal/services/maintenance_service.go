package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MaintenanceService struct {
	db       *mongo.Database
	domain   string // panel domain (e.g. panel.betazeninfotech.com)
	serverIP string // server public IP
}

func NewMaintenanceService(db *mongo.Database, panelDomain, serverIP string) *MaintenanceService {
	return &MaintenanceService{db: db, domain: panelDomain, serverIP: serverIP}
}

// GetStatus returns the current maintenance mode status for the server and all domains.
func (s *MaintenanceService) GetStatus(ctx context.Context) (map[string]interface{}, error) {
	status := make(map[string]interface{})

	// Check server-wide maintenance
	col := s.db.Collection(database.ColServerConfig)
	var config bson.M
	err := col.FindOne(ctx, bson.M{"key": "maintenance"}).Decode(&config)
	if err == mongo.ErrNoDocuments {
		status["server"] = map[string]interface{}{"enabled": false}
	} else if err != nil {
		return nil, err
	} else {
		status["server"] = config["value"]
	}

	// Check per-domain maintenance
	domainCol := s.db.Collection(database.ColDomains)
	cursor, err := domainCol.Find(ctx, bson.M{"maintenance_mode": true})
	if err == nil {
		defer cursor.Close(ctx)
		var domains []bson.M
		cursor.All(ctx, &domains)
		var domainList []string
		for _, d := range domains {
			if name, ok := d["domain"].(string); ok {
				domainList = append(domainList, name)
			}
		}
		status["domains"] = domainList
	}

	return status, nil
}

// buildMaintenanceHTML returns a styled maintenance page HTML.
func buildMaintenanceHTML(message string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Under Maintenance</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{min-height:100vh;display:flex;justify-content:center;align-items:center;font-family:-apple-system,BlinkMacSystemFont,sans-serif;background:#0f172a;color:#e2e8f0}
.container{text-align:center;padding:2rem;max-width:600px}
.icon{font-size:4rem;margin-bottom:1.5rem}
h1{font-size:2rem;margin-bottom:1rem;color:#f1f5f9}
p{font-size:1.1rem;line-height:1.6;color:#94a3b8}
.retry{margin-top:2rem;font-size:0.85rem;color:#64748b}
</style>
</head>
<body>
<div class="container">
<div class="icon">&#128736;</div>
<h1>Under Maintenance</h1>
<p>%s</p>
<p class="retry">We will be back shortly. Please try again later.</p>
</div>
</body>
</html>`, message)
}

// EnableServer enables server-wide maintenance mode with the given configuration.
func (s *MaintenanceService) EnableServer(ctx context.Context, config *models.MaintenanceConfig) error {
	config.Enabled = true

	// Store in DB
	col := s.db.Collection(database.ColServerConfig)
	_, err := col.UpdateOne(ctx,
		bson.M{"key": "maintenance"},
		bson.M{"$set": bson.M{"key": "maintenance", "value": config, "updated_at": time.Now()}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		return err
	}

	// Build the maintenance page HTML and write it to a file on disk
	// so nginx can serve it via error_page (avoids quoting issues in nginx config)
	message := config.Message
	if message == "" {
		message = "We are currently performing maintenance. Please check back soon."
	}

	maintenanceHTML := buildMaintenanceHTML(message)

	// Write the maintenance HTML page to disk
	if _, err := agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("mkdir -p /var/www/maintenance && cat > /var/www/maintenance/index.html << 'MAINT_EOF'\n%s\nMAINT_EOF", maintenanceHTML),
	); err != nil {
		return fmt.Errorf("failed to write maintenance page: %w", err)
	}

	// Build geo/map block for allowed IPs that bypass maintenance
	var mapEntries string
	for _, ip := range config.AllowedIPs {
		ip = strings.TrimSpace(ip)
		if ip != "" {
			mapEntries += fmt.Sprintf("    %s 0;\n", ip)
		}
	}

	// Build the list of server_names to EXCLUDE from maintenance
	// These will continue to work normally: panel domain + server IP
	var excludedNames []string
	if s.domain != "" && s.domain != "localhost" {
		excludedNames = append(excludedNames, s.domain)
	}
	if s.serverIP != "" {
		excludedNames = append(excludedNames, s.serverIP)
	}

	// Build the nginx maintenance config
	// Strategy: a default_server block that returns 503 for ALL domains
	// EXCEPT the panel domain and server IP (which have their own server blocks with higher priority)
	retryAfter := config.RetryAfter
	if retryAfter <= 0 {
		retryAfter = 3600
	}

	// Map block: $maintenance_mode = 1 by default, 0 for allowed IPs
	var nginxConf string
	if mapEntries != "" {
		nginxConf += fmt.Sprintf(`# Maintenance mode - allowed IP bypass
map $remote_addr $maintenance_mode {
    default 1;
%s}

`, mapEntries)
	}

	// HTTP (port 80) default_server — catches all unmatched domains
	nginxConf += fmt.Sprintf(`# Maintenance mode - HTTP catch-all
server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name _;

    # Serve maintenance page
    root /var/www/maintenance;

    location / {
%s        return 503;
    }

    error_page 503 @maintenance;
    location @maintenance {
        root /var/www/maintenance;
        add_header Retry-After %d always;
        add_header Content-Type "text/html; charset=UTF-8" always;
        rewrite ^(.*)$ /index.html break;
    }
}
`, s.buildIPBypassBlock(mapEntries != ""), retryAfter)

	// HTTPS (port 443) default_server — catches all unmatched SSL domains
	// Uses a self-signed/snakeoil cert for the catch-all (most servers have this)
	nginxConf += fmt.Sprintf(`
# Maintenance mode - HTTPS catch-all
server {
    listen 443 ssl default_server;
    listen [::]:443 ssl default_server;
    server_name _;

    # Use snakeoil cert for catch-all SSL (or any existing cert)
    ssl_certificate /etc/ssl/certs/ssl-cert-snakeoil.pem;
    ssl_certificate_key /etc/ssl/private/ssl-cert-snakeoil.key;

    root /var/www/maintenance;

    location / {
%s        return 503;
    }

    error_page 503 @maintenance;
    location @maintenance {
        root /var/www/maintenance;
        add_header Retry-After %d always;
        add_header Content-Type "text/html; charset=UTF-8" always;
        rewrite ^(.*)$ /index.html break;
    }
}
`, s.buildIPBypassBlock(mapEntries != ""), retryAfter)

	// Before writing, remove default_server from ALL existing site configs
	// so our maintenance config becomes the only default_server
	// This is critical — nginx refuses to have two default_servers on the same port
	if _, err := agent.RunCommand(ctx, "bash", "-c",
		"grep -rl 'default_server' /etc/nginx/sites-available/ 2>/dev/null | xargs -r sed -i 's/ default_server//g; s/default_server //g' 2>/dev/null; true",
	); err != nil {
		// Non-fatal — continue
	}
	if _, err := agent.RunCommand(ctx, "bash", "-c",
		"grep -rl 'default_server' /etc/nginx/sites-enabled/ 2>/dev/null | xargs -r sed -i 's/ default_server//g; s/default_server //g' 2>/dev/null; true",
	); err != nil {
		// Non-fatal — continue
	}

	// Also remove default_server from other conf.d files (except our own maintenance.conf)
	if _, err := agent.RunCommand(ctx, "bash", "-c",
		"for f in /etc/nginx/conf.d/*.conf; do [ \"$f\" != '/etc/nginx/conf.d/maintenance.conf' ] && sed -i 's/ default_server//g; s/default_server //g' \"$f\" 2>/dev/null; done; true",
	); err != nil {
		// Non-fatal
	}

	// Ensure snakeoil SSL cert exists for HTTPS catch-all
	agent.RunCommand(ctx, "bash", "-c",
		"dpkg -l ssl-cert >/dev/null 2>&1 || apt-get install -y ssl-cert >/dev/null 2>&1; "+
			"[ -f /etc/ssl/certs/ssl-cert-snakeoil.pem ] || make-ssl-cert generate-default-snakeoil >/dev/null 2>&1; true")

	// Write the nginx maintenance config using heredoc (safe for special chars)
	if _, err := agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("cat > /etc/nginx/conf.d/maintenance.conf << 'NGINX_EOF'\n%s\nNGINX_EOF", nginxConf),
	); err != nil {
		return fmt.Errorf("failed to write maintenance nginx config: %w", err)
	}

	// Test and reload nginx
	if err := agent.ReloadNginx(ctx); err != nil {
		// Rollback: remove broken config
		agent.RunCommand(ctx, "rm", "-f", "/etc/nginx/conf.d/maintenance.conf")
		agent.ReloadNginx(ctx)
		return fmt.Errorf("nginx reload failed (config rolled back): %w", err)
	}

	return nil
}

// buildIPBypassBlock returns nginx config lines that skip maintenance for allowed IPs.
func (s *MaintenanceService) buildIPBypassBlock(hasMap bool) string {
	if !hasMap {
		return ""
	}
	return `        if ($maintenance_mode = 0) {
            return 200;
        }
`
}

// DisableServer disables server-wide maintenance mode.
func (s *MaintenanceService) DisableServer(ctx context.Context) error {
	// Remove nginx maintenance config
	agent.RunCommand(ctx, "rm", "-f", "/etc/nginx/conf.d/maintenance.conf")

	// Restore default_server on the main site config if needed
	// (nginx will use the first server block as default if none is marked)

	if err := agent.ReloadNginx(ctx); err != nil {
		return err
	}

	// Update DB
	col := s.db.Collection(database.ColServerConfig)
	_, err := col.UpdateOne(ctx,
		bson.M{"key": "maintenance"},
		bson.M{"$set": bson.M{"value.enabled": false, "updated_at": time.Now()}},
	)
	return err
}

// EnableDomain enables maintenance mode for a specific domain.
func (s *MaintenanceService) EnableDomain(ctx context.Context, domain string, config *models.MaintenanceConfig) error {
	message := config.Message
	if message == "" {
		message = "This site is currently under maintenance."
	}

	maintenanceHTML := buildMaintenanceHTML(message)

	// Write per-domain maintenance page
	domainMaintDir := fmt.Sprintf("/var/www/maintenance/%s", domain)
	if _, err := agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("mkdir -p %s && cat > %s/index.html << 'MAINT_EOF'\n%s\nMAINT_EOF", domainMaintDir, domainMaintDir, maintenanceHTML),
	); err != nil {
		return fmt.Errorf("failed to write domain maintenance page: %w", err)
	}

	// Build allowed IPs variable block
	var allowedCheck string
	if len(config.AllowedIPs) > 0 {
		allowedCheck = "    set $maintenance 1;\n"
		for _, ip := range config.AllowedIPs {
			ip = strings.TrimSpace(ip)
			if ip != "" {
				allowedCheck += fmt.Sprintf("    if ($remote_addr = %s) { set $maintenance 0; }\n", ip)
			}
		}
	} else {
		allowedCheck = "    set $maintenance 1;\n"
	}

	maintenanceBlock := fmt.Sprintf(`
    # MAINTENANCE MODE START
%s
    if ($maintenance = 1) {
        return 503;
    }
    error_page 503 @domain_maintenance;
    location @domain_maintenance {
        root %s;
        add_header Content-Type "text/html; charset=UTF-8" always;
        rewrite ^(.*)$ /index.html break;
    }
    # MAINTENANCE MODE END
`, allowedCheck, domainMaintDir)

	// Inject into vhost after server_name line using heredoc-safe approach
	vhostPath := fmt.Sprintf("/etc/nginx/sites-available/%s", domain)

	// First remove any existing maintenance block
	agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("sed -i '/# MAINTENANCE MODE START/,/# MAINTENANCE MODE END/d' %s 2>/dev/null; true", vhostPath))
	agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("sed -i '/\\$maintenance/d' %s 2>/dev/null; true", vhostPath))

	// Create a temp file with the maintenance block and use sed to insert it
	tempFile := fmt.Sprintf("/tmp/maint_%s.txt", domain)
	if _, err := agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("cat > %s << 'MAINT_EOF'\n%s\nMAINT_EOF", tempFile, maintenanceBlock),
	); err != nil {
		return fmt.Errorf("failed to write temp maintenance block: %w", err)
	}

	// Insert after the first server_name line
	if _, err := agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("sed -i '/server_name/r %s' %s", tempFile, vhostPath),
	); err != nil {
		return fmt.Errorf("failed to inject maintenance block: %w", err)
	}

	// Clean up temp file
	agent.RunCommand(ctx, "rm", "-f", tempFile)

	if err := agent.ReloadNginx(ctx); err != nil {
		// Rollback
		agent.RunCommand(ctx, "bash", "-c",
			fmt.Sprintf("sed -i '/# MAINTENANCE MODE START/,/# MAINTENANCE MODE END/d' %s 2>/dev/null; true", vhostPath))
		agent.ReloadNginx(ctx)
		return fmt.Errorf("nginx reload failed for domain %s: %w", domain, err)
	}

	// Update domain in DB
	_, err := s.db.Collection(database.ColDomains).UpdateOne(ctx,
		bson.M{"domain": domain},
		bson.M{"$set": bson.M{"maintenance_mode": true}},
	)
	return err
}

// DisableDomain disables maintenance mode for a specific domain.
func (s *MaintenanceService) DisableDomain(ctx context.Context, domain string) error {
	vhostPath := fmt.Sprintf("/etc/nginx/sites-available/%s", domain)

	// Remove maintenance block from vhost
	agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("sed -i '/# MAINTENANCE MODE START/,/# MAINTENANCE MODE END/d' %s 2>/dev/null; true", vhostPath))
	agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("sed -i '/\\$maintenance/d' %s 2>/dev/null; true", vhostPath))

	// Clean up per-domain maintenance page
	agent.RunCommand(ctx, "rm", "-rf", fmt.Sprintf("/var/www/maintenance/%s", domain))

	if err := agent.ReloadNginx(ctx); err != nil {
		return err
	}

	_, err := s.db.Collection(database.ColDomains).UpdateOne(ctx,
		bson.M{"domain": domain},
		bson.M{"$set": bson.M{"maintenance_mode": false}},
	)
	return err
}
