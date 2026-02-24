package services

import (
	"context"

	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/mongo"
)

type NotificationService struct {
	db *mongo.Database
}

func NewNotificationService(db *mongo.Database) *NotificationService {
	return &NotificationService{db: db}
}

// GetSettings returns the current notification channel configuration.
func (s *NotificationService) GetSettings(ctx context.Context) (*models.NotificationSettings, error) {
	// TODO: implement - read notification settings from DB
	return nil, nil
}

// UpdateSettings saves the notification channel configuration.
func (s *NotificationService) UpdateSettings(ctx context.Context, settings *models.NotificationSettings) error {
	// TODO: implement - upsert notification settings in DB
	return nil
}

// History returns a paginated list of sent notification records.
func (s *NotificationService) History(ctx context.Context, page, limit int) ([]models.NotificationHistory, int64, error) {
	// TODO: implement - query notifications collection with pagination
	return nil, 0, nil
}

// ListWebhooks returns all configured notification webhooks.
func (s *NotificationService) ListWebhooks(ctx context.Context) ([]models.Webhook, error) {
	// TODO: implement - query webhooks collection
	return nil, nil
}

// CreateWebhook registers a new notification webhook endpoint.
func (s *NotificationService) CreateWebhook(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error) {
	// TODO: implement - store webhook record
	return nil, nil
}

// DeleteWebhook removes a notification webhook.
func (s *NotificationService) DeleteWebhook(ctx context.Context, id string) error {
	// TODO: implement - delete webhook record
	return nil
}

// TestWebhook sends a test payload to a webhook endpoint.
func (s *NotificationService) TestWebhook(ctx context.Context, id string) error {
	// TODO: implement - send test HTTP POST to webhook URL
	return nil
}
