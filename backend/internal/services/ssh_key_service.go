package services

import (
	"context"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/mongo"
)

type SSHKeyService struct {
	db *mongo.Database
}

func NewSSHKeyService(db *mongo.Database) *SSHKeyService {
	return &SSHKeyService{db: db}
}

// List returns all SSH keys for a given system user.
func (s *SSHKeyService) List(ctx context.Context, user string) ([]models.SSHKey, error) {
	// TODO: implement - query ssh_keys collection filtered by user
	return nil, nil
}

// Add installs a new SSH public key for a user.
func (s *SSHKeyService) Add(ctx context.Context, user string, req *models.AddSSHKeyRequest) (*models.SSHKey, error) {
	// TODO: implement - add key to authorized_keys, compute fingerprint, store record
	return nil, nil
}

// Delete removes an SSH key from a user's authorized keys.
func (s *SSHKeyService) Delete(ctx context.Context, user string, id string) error {
	// TODO: implement - remove key from authorized_keys, delete record
	return nil
}

// Generate creates a new SSH key pair for a user and returns the key data.
func (s *SSHKeyService) Generate(ctx context.Context, user string) (map[string]interface{}, error) {
	// TODO: implement - generate ed25519 key pair, add public key to authorized_keys
	return nil, nil
}
