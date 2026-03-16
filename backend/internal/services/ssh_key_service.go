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
	"go.mongodb.org/mongo-driver/bson/primitive"
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
	col := s.db.Collection(database.ColSSHKeys)
	cursor, err := col.Find(ctx, bson.M{"user": user})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var keys []models.SSHKey
	if err := cursor.All(ctx, &keys); err != nil {
		return nil, err
	}
	if keys == nil {
		keys = []models.SSHKey{}
	}
	return keys, nil
}

// Add installs a new SSH public key for a user.
func (s *SSHKeyService) Add(ctx context.Context, user string, req *models.AddSSHKeyRequest) (*models.SSHKey, error) {
	// Compute fingerprint
	var fingerprint string
	tmpFile := fmt.Sprintf("/tmp/sp-key-%d.pub", time.Now().UnixNano())
	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo '%s' > %s", req.PublicKey, tmpFile)); err == nil {
		if result, err := agent.RunCommand(ctx, "ssh-keygen", "-l", "-f", tmpFile); err == nil {
			parts := strings.Fields(result.Output)
			if len(parts) >= 2 {
				fingerprint = parts[1]
			}
		}
		os.Remove(tmpFile)
	}

	// Ensure .ssh directory exists
	sshDir := fmt.Sprintf("/home/%s/.ssh", user)
	if user == "root" {
		sshDir = "/root/.ssh"
	}
	agent.RunCommand(ctx, "mkdir", "-p", sshDir)
	agent.RunCommand(ctx, "chmod", "700", sshDir)

	// Append to authorized_keys
	authKeysPath := sshDir + "/authorized_keys"
	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo '%s' >> %s", req.PublicKey, authKeysPath)); err != nil {
		return nil, fmt.Errorf("failed to add key to authorized_keys: %w", err)
	}
	agent.RunCommand(ctx, "chmod", "600", authKeysPath)
	if user != "root" {
		agent.RunCommand(ctx, "chown", "-R", user+":"+user, sshDir)
	}

	key := models.SSHKey{
		User:        user,
		Name:        req.Name,
		PublicKey:    req.PublicKey,
		KeyType:     req.KeyType,
		Fingerprint: fingerprint,
		CreatedAt:   time.Now(),
	}

	result, err := s.db.Collection(database.ColSSHKeys).InsertOne(ctx, key)
	if err != nil {
		return nil, err
	}
	key.ID = result.InsertedID.(primitive.ObjectID)
	return &key, nil
}

// Delete removes an SSH key from a user's authorized keys.
func (s *SSHKeyService) Delete(ctx context.Context, user string, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid SSH key ID")
	}

	col := s.db.Collection(database.ColSSHKeys)
	var key models.SSHKey
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&key); err != nil {
		return fmt.Errorf("key not found")
	}

	// Remove from authorized_keys
	sshDir := fmt.Sprintf("/home/%s/.ssh", user)
	if user == "root" {
		sshDir = "/root/.ssh"
	}
	authKeysPath := sshDir + "/authorized_keys"

	// Escape the public key for grep
	safeKey := strings.ReplaceAll(key.PublicKey, "/", "\\/")
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -v '%s' %s > %s.tmp && mv %s.tmp %s",
		safeKey, authKeysPath, authKeysPath, authKeysPath, authKeysPath))

	_, err = col.DeleteOne(ctx, bson.M{"_id": oid})
	return err
}

// Generate creates a new SSH key pair for a user and returns the key data.
func (s *SSHKeyService) Generate(ctx context.Context, user string) (map[string]interface{}, error) {
	tmpPath := fmt.Sprintf("/tmp/sp-keygen-%d", time.Now().UnixNano())
	comment := fmt.Sprintf("%s@serverpanel", user)

	_, err := agent.RunCommand(ctx, "ssh-keygen", "-t", "ed25519", "-f", tmpPath, "-N", "", "-C", comment)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key pair: %w", err)
	}

	// Read keys
	result := make(map[string]interface{})
	if privResult, err := agent.RunCommand(ctx, "cat", tmpPath); err == nil {
		result["private_key"] = privResult.Output
	}
	if pubResult, err := agent.RunCommand(ctx, "cat", tmpPath+".pub"); err == nil {
		result["public_key"] = strings.TrimSpace(pubResult.Output)

		// Add to authorized_keys
		sshDir := fmt.Sprintf("/home/%s/.ssh", user)
		if user == "root" {
			sshDir = "/root/.ssh"
		}
		agent.RunCommand(ctx, "mkdir", "-p", sshDir)
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo '%s' >> %s/authorized_keys", strings.TrimSpace(pubResult.Output), sshDir))
		agent.RunCommand(ctx, "chmod", "600", sshDir+"/authorized_keys")

		// Get fingerprint
		if fpResult, err := agent.RunCommand(ctx, "ssh-keygen", "-l", "-f", tmpPath+".pub"); err == nil {
			parts := strings.Fields(fpResult.Output)
			if len(parts) >= 2 {
				result["fingerprint"] = parts[1]
			}
		}

		// Store in DB
		key := models.SSHKey{
			User:        user,
			Name:        "Generated Key",
			PublicKey:    strings.TrimSpace(pubResult.Output),
			KeyType:     "login",
			Fingerprint: fmt.Sprint(result["fingerprint"]),
			CreatedAt:   time.Now(),
		}
		s.db.Collection(database.ColSSHKeys).InsertOne(ctx, key)
	}

	// Cleanup temp files
	os.Remove(tmpPath)
	os.Remove(tmpPath + ".pub")

	return result, nil
}
