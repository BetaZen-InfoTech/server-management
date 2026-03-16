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
		if result, err := agent.RunCommand(ctx, "hostname"); err == nil {
			configs["hostname"] = strings.TrimSpace(result.Output)
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

func boolToOnOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
