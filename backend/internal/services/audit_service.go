package services

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
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

// List returns a paginated list of audit log entries.
func (s *AuditService) List(ctx context.Context, page, limit int, filters map[string]string) ([]models.AuditLog, int64, error) {
	col := s.db.Collection(database.ColAuditLogs)
	filter := bson.M{}

	if action, ok := filters["action"]; ok && action != "" {
		filter["action"] = action
	}
	if resource, ok := filters["resource"]; ok && resource != "" {
		filter["resource_type"] = resource
	}
	if userID, ok := filters["user_id"]; ok && userID != "" {
		filter["user.id"] = userID
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

// GetByID retrieves a single audit log entry by its ID.
func (s *AuditService) GetByID(ctx context.Context, id string) (*models.AuditLog, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid audit log ID")
	}
	var log models.AuditLog
	if err := s.db.Collection(database.ColAuditLogs).FindOne(ctx, bson.M{"_id": oid}).Decode(&log); err != nil {
		return nil, err
	}
	return &log, nil
}

// Export generates an export of audit logs in the requested format (csv, json).
func (s *AuditService) Export(ctx context.Context, format string, filters map[string]string) (string, error) {
	col := s.db.Collection(database.ColAuditLogs)
	filter := bson.M{}

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
