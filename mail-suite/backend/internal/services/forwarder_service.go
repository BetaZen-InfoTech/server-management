package services

import (
	"context"
	"errors"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/database"
	"github.com/betazeninfotech/mail-suite/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

var ErrForwarderNotFound = errors.New("forwarder not found")

type ForwarderService struct {
	db    *database.DB
	panel *BetazenPanelClient
}

func NewForwarderService(db *database.DB, panel *BetazenPanelClient) *ForwarderService {
	return &ForwarderService{db: db, panel: panel}
}

func (s *ForwarderService) List(ctx context.Context, userID primitive.ObjectID) ([]models.Forwarder, error) {
	cur, err := s.db.Col(database.ColForwarders).Find(ctx, bson.M{"user_id": userID})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []models.Forwarder{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *ForwarderService) Create(ctx context.Context, userID primitive.ObjectID, req models.ForwarderRequest) (*models.Forwarder, error) {
	accID, err := primitive.ObjectIDFromHex(req.AccountID)
	if err != nil {
		return nil, errors.New("invalid account_id")
	}
	f := models.Forwarder{
		UserID: userID, AccountID: accID,
		Source: req.Source, Destinations: req.Destinations,
		KeepCopy: req.KeepCopy, CreatedAt: time.Now(),
	}
	res, err := s.db.Col(database.ColForwarders).InsertOne(ctx, f)
	if err != nil {
		return nil, err
	}
	f.ID = res.InsertedID.(primitive.ObjectID)

	// Best-effort: push to Betazen panel forwarder table so the MTA picks it up.
	if s.panel != nil {
		_ = s.panel.CreateForwarder(ctx, req.Source, req.Destinations, req.KeepCopy)
	}
	return &f, nil
}

func (s *ForwarderService) Delete(ctx context.Context, userID, id primitive.ObjectID) error {
	var f models.Forwarder
	if err := s.db.Col(database.ColForwarders).FindOne(ctx, bson.M{"_id": id, "user_id": userID}).Decode(&f); err != nil {
		return ErrForwarderNotFound
	}
	if _, err := s.db.Col(database.ColForwarders).DeleteOne(ctx, bson.M{"_id": id}); err != nil {
		return err
	}
	if s.panel != nil {
		_ = s.panel.DeleteForwarder(ctx, f.Source)
	}
	return nil
}
