package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type NotificationService struct {
	db *mongo.Database
}

func NewNotificationService(db *mongo.Database) *NotificationService {
	return &NotificationService{db: db}
}

// GetSettings returns the current notification channel configuration.
func (s *NotificationService) GetSettings(ctx context.Context) (*models.NotificationSettings, error) {
	col := s.db.Collection(database.ColNotifications)
	var settings models.NotificationSettings
	err := col.FindOne(ctx, bson.M{}).Decode(&settings)
	if err == mongo.ErrNoDocuments {
		return &models.NotificationSettings{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

// UpdateSettings saves the notification channel configuration.
func (s *NotificationService) UpdateSettings(ctx context.Context, settings *models.NotificationSettings) error {
	col := s.db.Collection(database.ColNotifications)
	if settings.ID.IsZero() {
		// Insert new
		_, err := col.InsertOne(ctx, settings)
		return err
	}
	_, err := col.UpdateOne(ctx,
		bson.M{"_id": settings.ID},
		bson.M{"$set": settings},
		options.Update().SetUpsert(true),
	)
	return err
}

// History returns a paginated list of sent notification records.
func (s *NotificationService) History(ctx context.Context, page, limit int) ([]models.NotificationHistory, int64, error) {
	col := s.db.Collection("notification_history")
	total, err := col.CountDocuments(ctx, bson.M{})
	if err != nil {
		return nil, 0, err
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().SetSkip(skip).SetLimit(int64(limit)).SetSort(bson.D{{Key: "created_at", Value: -1}})
	cursor, err := col.Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var history []models.NotificationHistory
	if err := cursor.All(ctx, &history); err != nil {
		return nil, 0, err
	}
	if history == nil {
		history = []models.NotificationHistory{}
	}
	return history, total, nil
}

// ListWebhooks returns all configured notification webhooks.
func (s *NotificationService) ListWebhooks(ctx context.Context) ([]models.Webhook, error) {
	col := s.db.Collection(database.ColWebhooks)
	cursor, err := col.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var webhooks []models.Webhook
	if err := cursor.All(ctx, &webhooks); err != nil {
		return nil, err
	}
	if webhooks == nil {
		webhooks = []models.Webhook{}
	}
	return webhooks, nil
}

// CreateWebhook registers a new notification webhook endpoint.
func (s *NotificationService) CreateWebhook(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error) {
	webhook := models.Webhook{
		URL:       req.URL,
		Secret:    req.Secret,
		Events:    req.Events,
		Active:    req.Active,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	result, err := s.db.Collection(database.ColWebhooks).InsertOne(ctx, webhook)
	if err != nil {
		return nil, err
	}
	webhook.ID = result.InsertedID.(primitive.ObjectID)
	return &webhook, nil
}

// DeleteWebhook removes a notification webhook.
func (s *NotificationService) DeleteWebhook(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid webhook ID")
	}
	_, err = s.db.Collection(database.ColWebhooks).DeleteOne(ctx, bson.M{"_id": oid})
	return err
}

// TestWebhook sends a test payload to a webhook endpoint.
func (s *NotificationService) TestWebhook(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid webhook ID")
	}

	var webhook models.Webhook
	if err := s.db.Collection(database.ColWebhooks).FindOne(ctx, bson.M{"_id": oid}).Decode(&webhook); err != nil {
		return fmt.Errorf("webhook not found")
	}

	payload := map[string]interface{}{
		"event":     "test",
		"message":   "This is a test webhook from ServerPanel",
		"timestamp": time.Now().Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", webhook.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if webhook.Secret != "" {
		req.Header.Set("X-Webhook-Secret", webhook.Secret)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}
