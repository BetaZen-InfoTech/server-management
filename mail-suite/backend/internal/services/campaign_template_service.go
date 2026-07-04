package services

import (
	"context"
	"errors"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/database"
	"github.com/betazeninfotech/mail-suite/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var ErrTemplateNotFound = errors.New("template not found")

type CampaignTemplateService struct {
	db *database.DB
}

func NewCampaignTemplateService(db *database.DB) *CampaignTemplateService {
	return &CampaignTemplateService{db: db}
}

func (s *CampaignTemplateService) List(ctx context.Context, userID primitive.ObjectID) ([]models.CampaignTemplate, error) {
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	cur, err := s.db.Col(database.ColCampaignTemplates).Find(ctx, bson.M{"user_id": userID}, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []models.CampaignTemplate{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *CampaignTemplateService) Create(ctx context.Context, userID primitive.ObjectID, req models.CampaignTemplateRequest) (*models.CampaignTemplate, error) {
	now := time.Now()
	t := models.CampaignTemplate{
		UserID: userID, Name: req.Name, Subject: req.Subject, HTML: req.HTML,
		CreatedAt: now, UpdatedAt: now,
	}
	res, err := s.db.Col(database.ColCampaignTemplates).InsertOne(ctx, t)
	if err != nil {
		return nil, err
	}
	t.ID = res.InsertedID.(primitive.ObjectID)
	return &t, nil
}

func (s *CampaignTemplateService) Delete(ctx context.Context, userID, id primitive.ObjectID) error {
	res, err := s.db.Col(database.ColCampaignTemplates).DeleteOne(ctx, bson.M{"_id": id, "user_id": userID})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrTemplateNotFound
	}
	return nil
}
