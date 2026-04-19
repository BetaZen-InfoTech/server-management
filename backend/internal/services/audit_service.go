package services

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type AuditService struct {
	db *mongo.Database
}

func NewAuditService(db *mongo.Database) *AuditService {
	return &AuditService{db: db}
}

// applyAuditTenantScope merges a user.id-IN-tenant restriction into filter.
// Returns false when the caller is tenant-scoped AND no tenant user IDs
// could be resolved — caller must short-circuit to an empty result rather
// than fall through and risk leaking another tenant's audit trail.
//
// Resolves the tenant's user IDs by querying the users collection for
// every record whose tenant_id matches the caller's TenantHex. AuditLog
// stores user.id as a HEX string (the auth middleware copies the JWT's
// user_id claim verbatim) so we filter as a string $in list.
func applyAuditTenantScope(ctx context.Context, db *mongo.Database, filter bson.M) bool {
	scope := GetCallerScope(ctx)
	if scope == nil || !constants.IsTenantScoped(scope.Role) {
		return true // admin / unscoped — see everything
	}
	if scope.TenantHex == "" {
		return false
	}
	tenantOID, err := primitive.ObjectIDFromHex(scope.TenantHex)
	if err != nil {
		return false
	}
	cur, err := db.Collection(database.ColUsers).Find(ctx, bson.M{"tenant_id": tenantOID})
	if err != nil {
		return false
	}
	defer cur.Close(ctx)
	var ids []string
	for cur.Next(ctx) {
		var u struct {
			ID primitive.ObjectID `bson:"_id"`
		}
		if cur.Decode(&u) == nil {
			ids = append(ids, u.ID.Hex())
		}
	}
	if len(ids) == 0 {
		return false
	}
	filter["user.id"] = bson.M{"$in": ids}
	return true
}

// List returns a paginated list of audit log entries.
func (s *AuditService) List(ctx context.Context, page, limit int, filters map[string]string) ([]models.AuditLog, int64, error) {
	col := s.db.Collection(database.ColAuditLogs)
	filter := bson.M{}
	// Tenant scoping — vendor_owner sees every entry; everyone else only
	// sees entries whose user.id belongs to a user in their own tenant.
	// applyAuditTenantScope returns false when the caller is tenant-scoped
	// AND we couldn't resolve any tenant user IDs (defensive: better to
	// show nothing than leak someone else's audit trail).
	if !applyAuditTenantScope(ctx, s.db, filter) {
		return []models.AuditLog{}, 0, nil
	}

	if action, ok := filters["action"]; ok && action != "" {
		filter["action"] = action
	}
	if resource, ok := filters["resource"]; ok && resource != "" {
		filter["resource_type"] = resource
	}
	if userID, ok := filters["user_id"]; ok && userID != "" {
		// Existing user.id filter from the UI's "filter by user" dropdown
		// — combine with the tenant scope (intersect, not replace).
		if existing, has := filter["user.id"]; has {
			filter["$and"] = bson.A{
				bson.M{"user.id": existing},
				bson.M{"user.id": userID},
			}
			delete(filter, "user.id")
		} else {
			filter["user.id"] = userID
		}
	}
	if since, ok := filters["since"]; ok && since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			if _, exists := filter["timestamp"]; !exists {
				filter["timestamp"] = bson.M{}
			}
			filter["timestamp"].(bson.M)["$gte"] = t
		}
	}
	if until, ok := filters["until"]; ok && until != "" {
		if t, err := time.Parse(time.RFC3339, until); err == nil {
			if _, exists := filter["timestamp"]; !exists {
				filter["timestamp"] = bson.M{}
			}
			filter["timestamp"].(bson.M)["$lte"] = t
		}
	}

	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().SetSkip(skip).SetLimit(int64(limit)).SetSort(bson.D{{Key: "timestamp", Value: -1}})
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var logs []models.AuditLog
	if err := cursor.All(ctx, &logs); err != nil {
		return nil, 0, err
	}
	if logs == nil {
		logs = []models.AuditLog{}
	}
	return logs, total, nil
}

// GetByID retrieves a single audit log entry by its ID. Tenant-scoped so a
// vendor can't read another vendor's entry by guessing/scraping IDs.
func (s *AuditService) GetByID(ctx context.Context, id string) (*models.AuditLog, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid audit log ID")
	}
	filter := bson.M{"_id": oid}
	if !applyAuditTenantScope(ctx, s.db, filter) {
		return nil, fmt.Errorf("audit log not found")
	}
	var log models.AuditLog
	if err := s.db.Collection(database.ColAuditLogs).FindOne(ctx, filter).Decode(&log); err != nil {
		return nil, err
	}
	return &log, nil
}

// Export generates an export of audit logs in the requested format (csv, json).
func (s *AuditService) Export(ctx context.Context, format string, filters map[string]string) (string, error) {
	col := s.db.Collection(database.ColAuditLogs)
	filter := bson.M{}
	// Same tenant scoping as List — a vendor exporting CSV must not get
	// every other tenant's entries dumped into their download.
	if !applyAuditTenantScope(ctx, s.db, filter) {
		// Tenant-scoped caller with no resolvable users — return an empty
		// export rather than 500 so the UI's download still completes.
		switch format {
		case "json":
			return "[]", nil
		default:
			return "timestamp,user,action,resource,description,ip,status\n", nil
		}
	}

	if since, ok := filters["since"]; ok && since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			filter["timestamp"] = bson.M{"$gte": t}
		}
	}
	if until, ok := filters["until"]; ok && until != "" {
		if t, err := time.Parse(time.RFC3339, until); err == nil {
			if _, exists := filter["timestamp"]; !exists {
				filter["timestamp"] = bson.M{}
			}
			filter["timestamp"].(bson.M)["$lte"] = t
		}
	}

	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}}).SetLimit(10000)
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return "", err
	}
	defer cursor.Close(ctx)

	var logs []models.AuditLog
	if err := cursor.All(ctx, &logs); err != nil {
		return "", err
	}

	timestamp := time.Now().Format("20060102-150405")
	filePath := fmt.Sprintf("/tmp/audit-%s.%s", timestamp, format)

	switch format {
	case "csv":
		file, err := os.Create(filePath)
		if err != nil {
			return "", err
		}
		defer file.Close()

		writer := csv.NewWriter(file)
		defer writer.Flush()

		writer.Write([]string{"Timestamp", "User", "Action", "Resource Type", "Resource ID", "Description", "IP", "Status"})
		for _, log := range logs {
			writer.Write([]string{
				log.Timestamp.Format(time.RFC3339),
				log.User.Email,
				log.Action,
				log.ResourceType,
				log.ResourceID,
				log.Description,
				log.IPAddress,
				log.Status,
			})
		}
	case "json":
		file, err := os.Create(filePath)
		if err != nil {
			return "", err
		}
		defer file.Close()

		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(logs); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("unsupported format: %s", format)
	}

	return filePath, nil
}

// LogAction is an internal helper that creates a new audit log entry.
func (s *AuditService) LogAction(ctx context.Context, userID, email, role, action, resourceType, resourceID, description, ip, userAgent, status string, metadata map[string]interface{}) {
	entry := models.AuditLog{
		Timestamp:    time.Now(),
		User:         models.AuditUser{ID: userID, Email: email, Role: role},
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Description:  description,
		IPAddress:    ip,
		UserAgent:    userAgent,
		Status:       status,
		Metadata:     metadata,
	}
	_, _ = s.db.Collection(database.ColAuditLogs).InsertOne(ctx, entry)
}
