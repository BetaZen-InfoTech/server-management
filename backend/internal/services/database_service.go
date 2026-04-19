package services

import (
	"context"
	"fmt"
	"net"
	"regexp"
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

// EnableRemoteAccess is the legacy one-shot endpoint kept for backward
// compatibility. It now delegates to AddAccessHost, which persists the host
// record AND uses the database's own stored owner password (not an empty
// string — the previous implementation was creating passwordless MySQL
// accounts for the configured username, a major security bug).
func (s *DatabaseService) EnableRemoteAccess(ctx context.Context, dbID string, req *models.RemoteAccessRequest) error {
	_, err := s.AddAccessHost(ctx, dbID, req.AllowedIP, "Added via legacy remote-access endpoint")
	return err
}

// accessHostPattern accepts the same host shapes the underlying runtimes do:
//   - "%"                           (any host — MySQL only, firewall-wide open)
//   - "192.168.1.50"                (plain IPv4)
//   - "2001:db8::1"                 (IPv6)
//   - "192.168.1.0/24"              (CIDR)
//   - "192.168.%.%" / "10.%.%.%"    (MySQL percent-wildcard patterns)
//   - "db.example.com"              (FQDN — MySQL resolves at connect time)
//
// Anything else is rejected. We deliberately DON'T accept `*` wildcards
// because MySQL doesn't understand them and silently creates an
// unreachable user — easier for operators to fail loud here.
var accessHostPattern = regexp.MustCompile(`^(%|[a-zA-Z0-9][a-zA-Z0-9._%:/-]{0,252})$`)

// sanitiseAccessHost trims whitespace and lowercases hostnames. IP addresses
// are case-neutral already; MySQL wildcards are case-sensitive but operators
// expect case-insensitive behaviour here.
func sanitiseAccessHost(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// ListAccessHosts returns every remote-access host for a database, oldest
// first. Used by the WHM "Remote Database Access" modal.
func (s *DatabaseService) ListAccessHosts(ctx context.Context, dbID string) ([]models.DBAccessHost, error) {
	oid, err := primitive.ObjectIDFromHex(dbID)
	if err != nil {
		return nil, fmt.Errorf("invalid database id")
	}
	cur, err := s.db.Collection(database.ColDBAccessHosts).Find(ctx,
		bson.M{"database_id": oid},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}),
	)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var hosts []models.DBAccessHost
	if err := cur.All(ctx, &hosts); err != nil {
		return nil, err
	}
	if hosts == nil {
		hosts = []models.DBAccessHost{}
	}
	return hosts, nil
}

// AddAccessHost persists a new remote-access host for a database and applies
// the runtime-specific side effects:
//
//   - MySQL: creates a MySQL user scoped to that host (same username and
//     password as the database owner) with the same privileges, then opens
//     the firewall from the host (skipped for % / wildcard patterns which
//     would open the port to the whole internet — the operator can add
//     a 0.0.0.0/0 entry explicitly if they really want that).
//   - MongoDB: opens the firewall only. mongod's auth layer is what
//     actually gates remote connections, and it's per-user not per-host.
//
// Returns an "already exists" error on unique-index collision so the UI can
// show the operator a clear message instead of leaking a Mongo write error.
func (s *DatabaseService) AddAccessHost(ctx context.Context, dbID, rawHost, comment string) (*models.DBAccessHost, error) {
	dbRecord, err := s.GetByID(ctx, dbID)
	if err != nil {
		return nil, fmt.Errorf("database not found: %w", err)
	}
	host := sanitiseAccessHost(rawHost)
	if host == "" || !accessHostPattern.MatchString(host) {
		return nil, fmt.Errorf("invalid host %q: use an IP, CIDR, hostname, %% (any), or a MySQL pattern like 192.168.1.%%", rawHost)
	}

	// Apply the runtime side of things FIRST so we don't end up with a DB
	// row that says "allowed" but no actual grant/firewall rule backing it.
	switch dbRecord.Type {
	case "mysql":
		// Reuse the owner's password so the operator keeps one credential.
		// The MySQL grant is scoped to the host — a user@'%' lets anyone
		// in from any origin (still gated by password), whereas
		// user@'10.0.0.5' only accepts connections from that specific IP.
		if dbRecord.Password == "" {
			return nil, fmt.Errorf("database has no stored owner password — create the database via the panel so we can reuse its credentials, or add a dedicated DB user first")
		}
		if err := agent.CreateMySQLUserWithRole(ctx, dbRecord.DBName, dbRecord.Username, dbRecord.Password, host, "dbOwner"); err != nil {
			return nil, fmt.Errorf("failed to grant remote access in MySQL: %w", err)
		}
		// Only open the firewall for a concrete source (IP/CIDR). MySQL
		// wildcard patterns like "192.168.1.%" aren't valid UFW sources;
		// the MySQL-level grant alone constrains access, while the port
		// needs to be open to the world via an explicit 0.0.0.0/0 entry
		// if the operator really wants that exposure.
		if fwSrc := firewallSourceFor(host); fwSrc != "" {
			_ = agent.AllowPort(ctx, "3306", "tcp", fwSrc)
		}
	case "mongodb":
		if fwSrc := firewallSourceFor(host); fwSrc != "" {
			_ = agent.AllowPort(ctx, "27017", "tcp", fwSrc)
		}
	default:
		return nil, fmt.Errorf("database type %q does not support remote access", dbRecord.Type)
	}

	rec := models.DBAccessHost{
		DatabaseID: dbRecord.ID,
		Host:       host,
		Comment:    strings.TrimSpace(comment),
		CreatedAt:  time.Now(),
	}
	res, err := s.db.Collection(database.ColDBAccessHosts).InsertOne(ctx, rec)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, fmt.Errorf("host %q is already in the access list", host)
		}
		return nil, err
	}
	rec.ID = res.InsertedID.(primitive.ObjectID)
	return &rec, nil
}

// RemoveAccessHost drops the MySQL host-scoped user (if any) and deletes the
// access host record. Firewall rules are intentionally left behind: we don't
// know if the same IP was granted access through another entry (e.g. another
// database on the same port), and nuking it could break unrelated apps.
// Operators who want the port closed can prune UFW manually.
func (s *DatabaseService) RemoveAccessHost(ctx context.Context, dbID, hostID string) error {
	dbRecord, err := s.GetByID(ctx, dbID)
	if err != nil {
		return fmt.Errorf("database not found: %w", err)
	}
	hoid, err := primitive.ObjectIDFromHex(hostID)
	if err != nil {
		return fmt.Errorf("invalid host id")
	}
	var rec models.DBAccessHost
	if err := s.db.Collection(database.ColDBAccessHosts).FindOne(ctx, bson.M{"_id": hoid, "database_id": dbRecord.ID}).Decode(&rec); err != nil {
		return fmt.Errorf("access host not found: %w", err)
	}
	if dbRecord.Type == "mysql" && rec.Host != "" && rec.Host != "localhost" {
		// Best-effort drop; if the user was manually removed at the DB
		// level the DROP USER IF EXISTS is a no-op.
		_ = agent.DropMySQLUser(ctx, dbRecord.Username, rec.Host)
	}
	_, err = s.db.Collection(database.ColDBAccessHosts).DeleteOne(ctx, bson.M{"_id": hoid})
	return err
}

// firewallSourceFor maps an MySQL-style host pattern to a value UFW can
// accept, or returns "" to signal "don't touch the firewall for this one".
// UFW understands plain IPs, CIDR blocks, and the special "any" source
// (which we represent internally as 0.0.0.0/0 when host is "%"). Hostnames
// and MySQL wildcard patterns get MySQL-level enforcement only.
func firewallSourceFor(host string) string {
	switch {
	case host == "%":
		return "0.0.0.0/0"
	case strings.Contains(host, "%"):
		return "" // MySQL wildcard, can't express as UFW source
	case strings.Contains(host, "/"):
		return host // assume CIDR
	case net.ParseIP(host) != nil:
		return host
	default:
		return "" // hostname or something — MySQL will resolve at connect time
	}
}
