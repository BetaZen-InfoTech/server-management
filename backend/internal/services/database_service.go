package services

import (
	"context"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/mongo"
)

type DatabaseService struct {
	db *mongo.Database
}

func NewDatabaseService(db *mongo.Database) *DatabaseService {
	return &DatabaseService{db: db}
}

// List returns a paginated list of all managed databases.
func (s *DatabaseService) List(ctx context.Context, page, limit int) ([]models.Database, int64, error) {
	// TODO: implement - query databases collection with pagination
	return nil, 0, nil
}

// GetByID retrieves a single database record by its ID.
func (s *DatabaseService) GetByID(ctx context.Context, id string) (*models.Database, error) {
	// TODO: implement - find database by ObjectID
	return nil, nil
}

// Create provisions a new MongoDB database and its initial user.
func (s *DatabaseService) Create(ctx context.Context, req *models.CreateDatabaseRequest) (*models.Database, error) {
	// TODO: implement - create database, create user with roles, store record
	return nil, nil
}

// Delete removes a database and all associated users.
func (s *DatabaseService) Delete(ctx context.Context, id string) error {
	// TODO: implement - drop database, remove users, delete DB record
	return nil
}

// ListUsers returns all users associated with a specific database.
func (s *DatabaseService) ListUsers(ctx context.Context, dbID string) ([]models.DatabaseUser, error) {
	// TODO: implement - query database_users collection by database_id
	return nil, nil
}

// CreateUser adds a new user to a specific database with the given role.
func (s *DatabaseService) CreateUser(ctx context.Context, dbID string, req *models.CreateDBUserRequest) (*models.DatabaseUser, error) {
	// TODO: implement - create MongoDB user, store record
	return nil, nil
}

// DeleteUser removes a user from a database.
func (s *DatabaseService) DeleteUser(ctx context.Context, dbID string, userID string) error {
	// TODO: implement - drop MongoDB user, delete record
	return nil
}

// EnableRemoteAccess configures remote access for a database user from a specific IP.
func (s *DatabaseService) EnableRemoteAccess(ctx context.Context, dbID string, req *models.RemoteAccessRequest) error {
	// TODO: implement - update MongoDB bind config, add firewall rule
	return nil
}
