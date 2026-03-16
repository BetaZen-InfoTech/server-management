package services

import (
	"context"
	"fmt"
	"os"
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
	db *mongo.Database
}

func NewMaintenanceService(db *mongo.Database) *MaintenanceService {
	return &MaintenanceService{db: db}
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

	// Create nginx maintenance config
	message := config.Message
	if message == "" {
		message = "We are currently performing maintenance. Please check back soon."
	}

	var allowedBlock string
	for _, ip := range config.AllowedIPs {
		allowedBlock += fmt.Sprintf("    allow %s;\n", ip)
	}

	nginxConf := fmt.Sprintf(`server {
    listen 80 default_server;
    server_name _;
%s
    location / {
        return 503;
    }
    error_page 503 @maintenance;
    location @maintenance {
        default_type text/html;
        return 503 '<html><head><title>Maintenance</title></head><body style="display:flex;justify-content:center;align-items:center;min-height:100vh;font-family:sans-serif;"><div style="text-align:center;"><h1>Under Maintenance</h1><p>%s</p></div></body></html>';
    }
}
`, allowedBlock, message)

	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo '%s' > /etc/nginx/conf.d/maintenance.conf", nginxConf)); err != nil {
		return fmt.Errorf("failed to write maintenance config: %w", err)
	}

	return agent.ReloadNginx(ctx)
}

// DisableServer disables server-wide maintenance mode.
func (s *MaintenanceService) DisableServer(ctx context.Context) error {
	// Remove nginx config
	os.Remove("/etc/nginx/conf.d/maintenance.conf")

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

	// Add maintenance location to domain vhost
	maintenanceBlock := fmt.Sprintf(`
    # MAINTENANCE MODE
    if ($maintenance = 1) {
        return 503;
    }
    error_page 503 @maintenance;
    location @maintenance {
        default_type text/html;
        return 503 '<html><body><h1>Maintenance</h1><p>%s</p></body></html>';
    }
`, message)

	var allowedIPs string
	for _, ip := range config.AllowedIPs {
		allowedIPs += fmt.Sprintf("set $maintenance 1;\nif ($remote_addr = %s) { set $maintenance 0; }\n", ip)
	}
	if allowedIPs == "" {
		allowedIPs = "set $maintenance 1;\n"
	}

	// Inject into vhost
	vhostPath := fmt.Sprintf("/etc/nginx/sites-available/%s", domain)
	cmd := fmt.Sprintf("sed -i '/server_name/a\\%s%s' %s",
		strings.ReplaceAll(allowedIPs, "\n", "\\n"),
		strings.ReplaceAll(maintenanceBlock, "\n", "\\n"),
		vhostPath)
	agent.RunCommand(ctx, "bash", "-c", cmd)

	if err := agent.ReloadNginx(ctx); err != nil {
		return err
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
	// Remove maintenance block from vhost
	vhostPath := fmt.Sprintf("/etc/nginx/sites-available/%s", domain)
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/# MAINTENANCE MODE/,/@maintenance/d' %s", vhostPath))
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/\\$maintenance/d' %s", vhostPath))

	if err := agent.ReloadNginx(ctx); err != nil {
		return err
	}

	_, err := s.db.Collection(database.ColDomains).UpdateOne(ctx,
		bson.M{"domain": domain},
		bson.M{"$set": bson.M{"maintenance_mode": false}},
	)
	return err
}
