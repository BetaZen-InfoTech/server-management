package services

import (
	"context"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/mongo"
)

type ConfigService struct {
	db *mongo.Database
}

func NewConfigService(db *mongo.Database) *ConfigService {
	return &ConfigService{db: db}
}

// GetAll returns all server configuration sections (nginx, PHP, MongoDB, hostname).
func (s *ConfigService) GetAll(ctx context.Context) (map[string]interface{}, error) {
	// TODO: implement - read all config sections from server_config collection
	return nil, nil
}

// UpdateNginx applies updated Nginx configuration settings.
func (s *ConfigService) UpdateNginx(ctx context.Context, config *models.NginxConfig) error {
	// TODO: implement - write nginx.conf, validate, store in DB
	return nil
}

// UpdatePHP applies updated PHP-FPM configuration settings.
func (s *ConfigService) UpdatePHP(ctx context.Context, config *models.PHPConfig) error {
	// TODO: implement - write php.ini settings, reload PHP-FPM, store in DB
	return nil
}

// UpdateMongoDB applies updated MongoDB configuration settings.
func (s *ConfigService) UpdateMongoDB(ctx context.Context, config *models.MongoDBConfig) error {
	// TODO: implement - write mongod.conf, restart MongoDB, store in DB
	return nil
}

// UpdateHostname changes the server's hostname.
func (s *ConfigService) UpdateHostname(ctx context.Context, hostname string) error {
	// TODO: implement - set hostname via hostnamectl, update /etc/hosts
	return nil
}

// TestNginx validates the current Nginx configuration.
func (s *ConfigService) TestNginx(ctx context.Context) (map[string]interface{}, error) {
	// TODO: implement - run nginx -t and return success/error output
	return nil, nil
}

// RestartService restarts a managed server service by name.
func (s *ConfigService) RestartService(ctx context.Context, serviceName string) error {
	// TODO: implement - run systemctl restart <service>
	return nil
}
