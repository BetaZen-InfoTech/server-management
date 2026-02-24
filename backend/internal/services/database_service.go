package services

import (
	"context"
	"fmt"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type DatabaseService struct {
	db *mongo.Database
}

func NewDatabaseService(db *mongo.Database) *DatabaseService {
	return &DatabaseService{db: db}
}

func (s *DatabaseService) List(ctx context.Context, page, limit int) ([]models.Database, int64, error) {
	col := s.db.Collection(database.ColDatabases)
	filter := bson.M{}

	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().SetSkip(skip).SetLimit(int64(limit)).SetSort(bson.D{{Key: "created_at", Value: -1}})
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var dbs []models.Database
	if err := cursor.All(ctx, &dbs); err != nil {
		return nil, 0, err
	}
	if dbs == nil {
		dbs = []models.Database{}
	}
	return dbs, total, nil
}

func (s *DatabaseService) GetByID(ctx context.Context, id string) (*models.Database, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid database ID")
	}
	col := s.db.Collection(database.ColDatabases)
	var db models.Database
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&db); err != nil {
		return nil, err
	}
	return &db, nil
}

func (s *DatabaseService) Create(ctx context.Context, req *models.CreateDatabaseRequest) (*models.Database, error) {
	dbType := req.Type
	if dbType == "" {
		dbType = "mongodb"
	}

	var host string
	var port int
	var connStr string

	switch dbType {
	case "mongodb":
		if err := agent.CreateMongoDatabase(ctx, req.DBName, req.Username, req.Password); err != nil {
			return nil, fmt.Errorf("failed to create MongoDB database: %w", err)
		}
		host = "localhost"
		port = 27017
		connStr = fmt.Sprintf("mongodb://%s:%s@localhost:27017/%s", req.Username, req.Password, req.DBName)
	case "mysql":
		if err := agent.CreateMySQLDatabase(ctx, req.DBName); err != nil {
			return nil, fmt.Errorf("failed to create MySQL database: %w", err)
		}
		if err := agent.CreateMySQLUser(ctx, req.DBName, req.Username, req.Password, "localhost"); err != nil {
			return nil, fmt.Errorf("failed to create MySQL user: %w", err)
		}
		host = "localhost"
		port = 3306
		connStr = fmt.Sprintf("mysql://%s:%s@localhost:3306/%s", req.Username, req.Password, req.DBName)
	default:
		return nil, fmt.Errorf("unsupported database type: %s", dbType)
	}

	now := time.Now()
	dbRecord := models.Database{
		DBName:           req.DBName,
		Type:             dbType,
		Username:         req.Username,
		Password:         req.Password,
		Domain:           req.Domain,
		Host:             host,
		Port:             port,
		ConnectionString: connStr,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	col := s.db.Collection(database.ColDatabases)
	result, err := col.InsertOne(ctx, dbRecord)
	if err != nil {
		return nil, err
	}
	dbRecord.ID = result.InsertedID.(primitive.ObjectID)

	// Create initial user record
	userRecord := models.DatabaseUser{
		DatabaseID: dbRecord.ID,
		Username:   req.Username,
		Password:   req.Password,
		Role:       "readWrite",
		CreatedAt:  now,
	}
	s.db.Collection(database.ColDBUsers).InsertOne(ctx, userRecord)

	return &dbRecord, nil
}

func (s *DatabaseService) Delete(ctx context.Context, id string) error {
	dbRecord, err := s.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("database not found: %w", err)
	}

	switch dbRecord.Type {
	case "mongodb":
		if err := agent.DeleteMongoDatabase(ctx, dbRecord.DBName); err != nil {
			return fmt.Errorf("failed to drop MongoDB database: %w", err)
		}
	case "mysql":
		if err := agent.DropMySQLDatabase(ctx, dbRecord.DBName); err != nil {
			return fmt.Errorf("failed to drop MySQL database: %w", err)
		}
		// Also drop all MySQL users for this database
		userCol := s.db.Collection(database.ColDBUsers)
		cursor, _ := userCol.Find(ctx, bson.M{"database_id": dbRecord.ID})
		if cursor != nil {
			var users []models.DatabaseUser
			cursor.All(ctx, &users)
			for _, u := range users {
				agent.DropMySQLUser(ctx, u.Username, "localhost")
			}
			cursor.Close(ctx)
		}
	}

	// Delete all associated users from our database
	s.db.Collection(database.ColDBUsers).DeleteMany(ctx, bson.M{"database_id": dbRecord.ID})

	// Delete the database record
	col := s.db.Collection(database.ColDatabases)
	_, err = col.DeleteOne(ctx, bson.M{"_id": dbRecord.ID})
	return err
}

func (s *DatabaseService) ListUsers(ctx context.Context, dbID string) ([]models.DatabaseUser, error) {
	oid, err := primitive.ObjectIDFromHex(dbID)
	if err != nil {
		return nil, fmt.Errorf("invalid database ID")
	}

	col := s.db.Collection(database.ColDBUsers)
	cursor, err := col.Find(ctx, bson.M{"database_id": oid})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var users []models.DatabaseUser
	if err := cursor.All(ctx, &users); err != nil {
		return nil, err
	}
	if users == nil {
		users = []models.DatabaseUser{}
	}
	return users, nil
}

func (s *DatabaseService) CreateUser(ctx context.Context, dbID string, req *models.CreateDBUserRequest) (*models.DatabaseUser, error) {
	dbRecord, err := s.GetByID(ctx, dbID)
	if err != nil {
		return nil, fmt.Errorf("database not found: %w", err)
	}

	switch dbRecord.Type {
	case "mongodb":
		if err := agent.CreateMongoUser(ctx, dbRecord.DBName, req.Username, req.Password, req.Role); err != nil {
			return nil, fmt.Errorf("failed to create MongoDB user: %w", err)
		}
	case "mysql":
		if err := agent.CreateMySQLUser(ctx, dbRecord.DBName, req.Username, req.Password, "localhost"); err != nil {
			return nil, fmt.Errorf("failed to create MySQL user: %w", err)
		}
	}

	user := models.DatabaseUser{
		DatabaseID: dbRecord.ID,
		Username:   req.Username,
		Password:   req.Password,
		Role:       req.Role,
		CreatedAt:  time.Now(),
	}

	col := s.db.Collection(database.ColDBUsers)
	result, err := col.InsertOne(ctx, user)
	if err != nil {
		return nil, err
	}
	user.ID = result.InsertedID.(primitive.ObjectID)
	return &user, nil
}

func (s *DatabaseService) DeleteUser(ctx context.Context, dbID string, userID string) error {
	dbRecord, err := s.GetByID(ctx, dbID)
	if err != nil {
		return fmt.Errorf("database not found: %w", err)
	}

	userOID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return fmt.Errorf("invalid user ID")
	}

	col := s.db.Collection(database.ColDBUsers)
	var user models.DatabaseUser
	if err := col.FindOne(ctx, bson.M{"_id": userOID}).Decode(&user); err != nil {
		return fmt.Errorf("user not found")
	}

	switch dbRecord.Type {
	case "mongodb":
		if err := agent.DeleteMongoUser(ctx, dbRecord.DBName, user.Username); err != nil {
			return fmt.Errorf("failed to delete MongoDB user: %w", err)
		}
	case "mysql":
		if err := agent.DropMySQLUser(ctx, user.Username, "localhost"); err != nil {
			return fmt.Errorf("failed to delete MySQL user: %w", err)
		}
	}

	_, err = col.DeleteOne(ctx, bson.M{"_id": userOID})
	return err
}

func (s *DatabaseService) EnableRemoteAccess(ctx context.Context, dbID string, req *models.RemoteAccessRequest) error {
	dbRecord, err := s.GetByID(ctx, dbID)
	if err != nil {
		return fmt.Errorf("database not found: %w", err)
	}

	switch dbRecord.Type {
	case "mongodb":
		// Allow MongoDB port from specific IP
		if err := agent.AllowPort(ctx, "27017", "tcp", req.AllowedIP); err != nil {
			return fmt.Errorf("failed to allow firewall port: %w", err)
		}
	case "mysql":
		// Grant remote access for MySQL user
		if err := agent.CreateMySQLUser(ctx, dbRecord.DBName, req.Username, "", req.AllowedIP); err != nil {
			return fmt.Errorf("failed to grant remote access: %w", err)
		}
		if err := agent.AllowPort(ctx, "3306", "tcp", req.AllowedIP); err != nil {
			return fmt.Errorf("failed to allow firewall port: %w", err)
		}
	}

	return nil
}
