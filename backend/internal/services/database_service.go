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
	if scope := GetCallerScope(ctx); scope != nil {
		filter = scope.ApplyDomainScope(ctx, s.db, "domain", filter)
	}

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
	var dbDoc models.Database
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&dbDoc); err != nil {
		return nil, err
	}
	if scope := GetCallerScope(ctx); scope != nil {
		if err := scope.AssertOwnsDomain(ctx, s.db, dbDoc.Domain); err != nil {
			return nil, fmt.Errorf("database not found")
		}
	}
	return &dbDoc, nil
}

func (s *DatabaseService) Create(ctx context.Context, req *models.CreateDatabaseRequest) (*models.Database, error) {
	dbType := req.Type
	if dbType == "" {
		dbType = "mongodb"
	}

	// Enforce username prefix on db_name and username based on domain owner
	domCol := s.db.Collection(database.ColDomains)
	var dom models.Domain
	if err := domCol.FindOne(ctx, bson.M{"domain": req.Domain}).Decode(&dom); err == nil && dom.User != "" {
		prefix := dom.User + "_"
		if !strings.HasPrefix(req.DBName, prefix) {
			req.DBName = prefix + req.DBName
		}
		if !strings.HasPrefix(req.Username, prefix) {
			req.Username = prefix + req.Username
		}
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
		if err := agent.CreateMySQLUserWithRole(ctx, req.DBName, req.Username, req.Password, "localhost", "dbOwner"); err != nil {
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
		if err := agent.CreateMySQLUserWithRole(ctx, dbRecord.DBName, req.Username, req.Password, "localhost", req.Role); err != nil {
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

// UpdateOwnerPassword changes the password of the primary database owner (stored
// on the Database record) at both the engine level and the local DatabaseUser entry.
func (s *DatabaseService) UpdateOwnerPassword(ctx context.Context, dbID, newPassword string) error {
	dbRecord, err := s.GetByID(ctx, dbID)
	if err != nil {
		return fmt.Errorf("database not found: %w", err)
	}

	switch dbRecord.Type {
	case "mongodb":
		if err := agent.UpdateMongoUserPassword(ctx, dbRecord.DBName, dbRecord.Username, newPassword); err != nil {
			return fmt.Errorf("failed to update MongoDB password: %w", err)
		}
	case "mysql":
		if err := agent.UpdateMySQLUserPassword(ctx, dbRecord.Username, "localhost", newPassword); err != nil {
			return fmt.Errorf("failed to update MySQL password: %w", err)
		}
	}

	connStr := buildConnectionString(dbRecord.Type, dbRecord.Username, newPassword, dbRecord.Host, dbRecord.Port, dbRecord.DBName)
	_, err = s.db.Collection(database.ColDatabases).UpdateOne(ctx,
		bson.M{"_id": dbRecord.ID},
		bson.M{"$set": bson.M{
			"password":          newPassword,
			"connection_string": connStr,
			"updated_at":        time.Now(),
		}},
	)
	if err != nil {
		return err
	}

	// Sync the matching DatabaseUser entry (the one that mirrors the owner) too.
	s.db.Collection(database.ColDBUsers).UpdateMany(ctx,
		bson.M{"database_id": dbRecord.ID, "username": dbRecord.Username},
		bson.M{"$set": bson.M{"password": newPassword}},
	)
	return nil
}

func (s *DatabaseService) UpdateUserPassword(ctx context.Context, dbID, userID, newPassword string) error {
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
	if err := col.FindOne(ctx, bson.M{"_id": userOID, "database_id": dbRecord.ID}).Decode(&user); err != nil {
		return fmt.Errorf("user not found")
	}

	switch dbRecord.Type {
	case "mongodb":
		if err := agent.UpdateMongoUserPassword(ctx, dbRecord.DBName, user.Username, newPassword); err != nil {
			return fmt.Errorf("failed to update MongoDB password: %w", err)
		}
	case "mysql":
		if err := agent.UpdateMySQLUserPassword(ctx, user.Username, "localhost", newPassword); err != nil {
			return fmt.Errorf("failed to update MySQL password: %w", err)
		}
	}

	_, err = col.UpdateOne(ctx, bson.M{"_id": userOID}, bson.M{"$set": bson.M{"password": newPassword}})
	if err != nil {
		return err
	}

	// If this user mirrors the database owner, sync the parent record too.
	if user.Username == dbRecord.Username {
		connStr := buildConnectionString(dbRecord.Type, dbRecord.Username, newPassword, dbRecord.Host, dbRecord.Port, dbRecord.DBName)
		s.db.Collection(database.ColDatabases).UpdateOne(ctx,
			bson.M{"_id": dbRecord.ID},
			bson.M{"$set": bson.M{"password": newPassword, "connection_string": connStr, "updated_at": time.Now()}},
		)
	}
	return nil
}

func (s *DatabaseService) UpdateUserRole(ctx context.Context, dbID, userID, role string) error {
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
	if err := col.FindOne(ctx, bson.M{"_id": userOID, "database_id": dbRecord.ID}).Decode(&user); err != nil {
		return fmt.Errorf("user not found")
	}

	switch dbRecord.Type {
	case "mongodb":
		if err := agent.UpdateMongoUserRole(ctx, dbRecord.DBName, user.Username, role); err != nil {
			return fmt.Errorf("failed to update MongoDB role: %w", err)
		}
	case "mysql":
		if err := agent.UpdateMySQLUserRole(ctx, dbRecord.DBName, user.Username, "localhost", role); err != nil {
			return fmt.Errorf("failed to update MySQL grants: %w", err)
		}
	}

	_, err = col.UpdateOne(ctx, bson.M{"_id": userOID}, bson.M{"$set": bson.M{"role": role}})
	return err
}

// GetConnectionInfo returns the full connection details for a database, including
// the plaintext password. Caller must have database.view permission.
func (s *DatabaseService) GetConnectionInfo(ctx context.Context, dbID string) (*models.ConnectionInfoResponse, error) {
	dbRecord, err := s.GetByID(ctx, dbID)
	if err != nil {
		return nil, err
	}

	connStr := dbRecord.ConnectionString
	if connStr == "" {
		connStr = buildConnectionString(dbRecord.Type, dbRecord.Username, dbRecord.Password, dbRecord.Host, dbRecord.Port, dbRecord.DBName)
	}

	cli := ""
	switch dbRecord.Type {
	case "mongodb":
		cli = fmt.Sprintf(`mongosh "%s"`, connStr)
	case "mysql":
		cli = fmt.Sprintf(`mysql -h %s -P %d -u %s -p%s %s`, dbRecord.Host, dbRecord.Port, dbRecord.Username, dbRecord.Password, dbRecord.DBName)
	}

	return &models.ConnectionInfoResponse{
		Type:             dbRecord.Type,
		Host:             dbRecord.Host,
		Port:             dbRecord.Port,
		Database:         dbRecord.DBName,
		Username:         dbRecord.Username,
		Password:         dbRecord.Password,
		ConnectionString: connStr,
		CLICommand:       cli,
	}, nil
}

// GetPhpMyAdminInfo returns phpMyAdmin login details for a MySQL database.
func (s *DatabaseService) GetPhpMyAdminInfo(ctx context.Context, dbID, baseURL string) (*models.PhpMyAdminResponse, error) {
	dbRecord, err := s.GetByID(ctx, dbID)
	if err != nil {
		return nil, err
	}
	if dbRecord.Type != "mysql" {
		return nil, fmt.Errorf("phpMyAdmin is only available for MySQL databases")
	}
	if baseURL == "" {
		baseURL = "/phpmyadmin/"
	}
	return &models.PhpMyAdminResponse{
		URL:      baseURL,
		Username: dbRecord.Username,
		Password: dbRecord.Password,
		Database: dbRecord.DBName,
		Server:   dbRecord.Host,
	}, nil
}

func buildConnectionString(dbType, user, pass, host string, port int, name string) string {
	switch dbType {
	case "mongodb":
		return fmt.Sprintf("mongodb://%s:%s@%s:%d/%s", user, pass, host, port, name)
	case "mysql":
		return fmt.Sprintf("mysql://%s:%s@%s:%d/%s", user, pass, host, port, name)
	}
	return ""
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
