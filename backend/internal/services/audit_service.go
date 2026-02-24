package services

import (
	"context"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/mongo"
)

type AuditService struct {
	db *mongo.Database
}

func NewAuditService(db *mongo.Database) *AuditService {
	return &AuditService{db: db}
}

// List returns a paginated list of audit log entries.
func (s *AuditService) List(ctx context.Context, page, limit int, filters map[string]string) ([]models.AuditLog, int64, error) {
	// TODO: implement - query audit_logs collection with pagination and optional filters
	return nil, 0, nil
}

// GetByID retrieves a single audit log entry by its ID.
func (s *AuditService) GetByID(ctx context.Context, id string) (*models.AuditLog, error) {
	// TODO: implement - find audit log entry by ObjectID
	return nil, nil
}

// Export generates an export of audit logs in the requested format (csv, json).
func (s *AuditService) Export(ctx context.Context, format string, filters map[string]string) (string, error) {
	// TODO: implement - query logs, serialize to format, write temp file, return path
	return "", nil
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
