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

// panelConfigName returns the nginx config filename for the server panel.
// The panel config is the ONLY site that stays enabled during maintenance.
const panelConfigName = "serverpanel"

// backupDir is where we stash disabled site symlinks during maintenance.
const maintenanceBackupDir = "/etc/nginx/.maintenance-disabled"

// EnableServer enables server-wide maintenance mode.
// Strategy:
//  1. Write a maintenance HTML page to /var/www/maintenance/
//  2. Move ALL site symlinks from sites-enabled/ to a backup dir
//     EXCEPT the panel config ("serverpanel") so the admin panel stays up
//  3. Write a catch-all nginx config that returns 503 for everything else
//  4. Reload nginx
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

	message := config.Message
	if message == "" {
		message = "We are currently performing maintenance. Please check back soon."
	}

	// Write the maintenance HTML page to disk
	maintenanceHTML := buildMaintenanceHTML(message)
	if _, err := agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("mkdir -p /var/www/maintenance && cat > /var/www/maintenance/index.html << 'MAINT_EOF'\n%s\nMAINT_EOF", maintenanceHTML),
	); err != nil {
		return fmt.Errorf("failed to write maintenance page: %w", err)
	}

	// Create backup directory for disabled sites
	agent.RunCommand(ctx, "mkdir", "-p", maintenanceBackupDir)

	// Move ALL site configs from sites-enabled/ to backup dir
	// EXCEPT: "serverpanel" (the panel must stay live)
	moveCmd := fmt.Sprintf(
		`for f in /etc/nginx/sites-enabled/*; do
			name=$(basename "$f")
			if [ "$name" != "%s" ]; then
				mv "$f" "%s/$name" 2>/dev/null || true
			fi
		done`,
		panelConfigName, maintenanceBackupDir,
	)
	if _, err := agent.RunCommand(ctx, "bash", "-c", moveCmd); err != nil {
		return fmt.Errorf("failed to disable site configs: %w", err)
	}

	// Strip `default_server` from the panel vhost while maintenance is
	// on. The maintenance catch-all below uses `listen 80 default_server`
	// to trap every unmapped request and return 503; nginx refuses to
	// load with two default_servers on the same (addr,port), which was
	// the live failure ("a duplicate default server for 0.0.0.0:80 in
	// /etc/nginx/sites-enabled/serverpanel:2"). Backup the pre-edit
	// file so DisableServer can put `default_server` back exactly the
	// way the operator had it. awk rewrites only `listen ... default_server`
	// lines and leaves everything else untouched.
	stripCmd := fmt.Sprintf(
		`f=/etc/nginx/sites-available/%s
		[ -f "$f" ] || exit 0
		cp "$f" "%s/%s.pre-maintenance" 2>/dev/null || true
		awk 'match($0,/listen[[:space:]]+[^;]*default_server/) { sub(/[[:space:]]+default_server/,""); print; next } {print}' "$f" > "$f.tmp" && mv "$f.tmp" "$f"`,
		panelConfigName, maintenanceBackupDir, panelConfigName,
	)
	if _, err := agent.RunCommand(ctx, "bash", "-c", stripCmd); err != nil {
		return fmt.Errorf("failed to strip default_server from panel vhost: %w", err)
	}

	// Build the nginx maintenance catch-all config
	retryAfter := config.RetryAfter
	if retryAfter <= 0 {
		retryAfter = 3600
	}

	// Build allowed IPs map block
	var mapBlock string
	hasAllowedIPs := false
	if len(config.AllowedIPs) > 0 {
		var entries string
		for _, ip := range config.AllowedIPs {
			ip = strings.TrimSpace(ip)
			if ip != "" {
				entries += fmt.Sprintf("    %s 0;\n", ip)
				hasAllowedIPs = true
			}
		}
		if hasAllowedIPs {
			mapBlock = fmt.Sprintf("map $remote_addr $maintenance_bypass {\n    default 1;\n%s}\n\n", entries)
		}
	}

	ipBypass := ""
	if hasAllowedIPs {
		ipBypass = `        if ($maintenance_bypass = 0) {
            return 200;
        }
`
	}

	// Ensure snakeoil SSL cert exists
	agent.RunCommand(ctx, "bash", "-c",
		"[ -f /etc/ssl/certs/ssl-cert-snakeoil.pem ] || (apt-get install -y ssl-cert >/dev/null 2>&1 && make-ssl-cert generate-default-snakeoil >/dev/null 2>&1); true")

	nginxConf := fmt.Sprintf(`%s# Maintenance mode - catch-all for HTTP
server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name _;
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

# Maintenance mode - catch-all for HTTPS
server {
    listen 443 ssl default_server;
    listen [::]:443 ssl default_server;
    server_name _;
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
`, mapBlock, ipBypass, retryAfter, ipBypass, retryAfter)

	// Write nginx maintenance config
	if _, err := agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("cat > /etc/nginx/conf.d/maintenance.conf << 'NGINX_EOF'\n%s\nNGINX_EOF", nginxConf),
	); err != nil {
		return fmt.Errorf("failed to write maintenance nginx config: %w", err)
	}

	// Test and reload nginx
	if err := agent.ReloadNginx(ctx); err != nil {
		// Rollback: restore all site configs (incl. the panel vhost's
		// pre-maintenance default_server line) and remove maintenance config
		restorePanel := fmt.Sprintf(
			`b=%s/%s.pre-maintenance
			if [ -f "$b" ]; then
				mv "$b" /etc/nginx/sites-available/%s 2>/dev/null || true
			fi`,
			maintenanceBackupDir, panelConfigName, panelConfigName,
		)
		agent.RunCommand(ctx, "bash", "-c", restorePanel)
		s.restoreSiteConfigs(ctx)
		agent.RunCommand(ctx, "rm", "-f", "/etc/nginx/conf.d/maintenance.conf")
		agent.ReloadNginx(ctx)
		return fmt.Errorf("nginx reload failed (rolled back): %w", err)
	}

	return nil
}

// DisableServer disables server-wide maintenance mode.
func (s *MaintenanceService) DisableServer(ctx context.Context) error {
	// Remove nginx maintenance config
	agent.RunCommand(ctx, "rm", "-f", "/etc/nginx/conf.d/maintenance.conf")

	// Restore the panel vhost from its pre-maintenance backup so
	// `default_server` comes back on its listen line. We do this BEFORE
	// restoreSiteConfigs moves the other sites back in case anything in
	// there also uses default_server (there shouldn't be — tenants
	// never get default_server — but paranoia is cheap here).
	restorePanel := fmt.Sprintf(
		`b=%s/%s.pre-maintenance
		if [ -f "$b" ]; then
			mv "$b" /etc/nginx/sites-available/%s 2>/dev/null || true
		fi`,
		maintenanceBackupDir, panelConfigName, panelConfigName,
	)
	agent.RunCommand(ctx, "bash", "-c", restorePanel)

	// Restore all disabled site configs back to sites-enabled/
	s.restoreSiteConfigs(ctx)

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

// restoreSiteConfigs moves all backed-up site configs from the backup dir
// back to sites-enabled/.
func (s *MaintenanceService) restoreSiteConfigs(ctx context.Context) {
	restoreCmd := fmt.Sprintf(
		`if [ -d "%s" ]; then
			for f in %s/*; do
				[ -f "$f" ] && mv "$f" /etc/nginx/sites-enabled/ 2>/dev/null || true
			done
			rmdir "%s" 2>/dev/null || true
		fi`,
		maintenanceBackupDir, maintenanceBackupDir, maintenanceBackupDir,
	)
	agent.RunCommand(ctx, "bash", "-c", restoreCmd)
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

	agent.RunCommand(ctx, "rm", "-f", tempFile)

	if err := agent.ReloadNginx(ctx); err != nil {
		// Rollback
		agent.RunCommand(ctx, "bash", "-c",
			fmt.Sprintf("sed -i '/# MAINTENANCE MODE START/,/# MAINTENANCE MODE END/d' %s 2>/dev/null; true", vhostPath))
		agent.ReloadNginx(ctx)
		return fmt.Errorf("nginx reload failed for domain %s: %w", domain, err)
	}

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
