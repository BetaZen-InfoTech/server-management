package services

import (
	"context"
	"errors"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/database"
	"github.com/betazeninfotech/mail-suite/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var ErrDraftNotFound = errors.New("draft not found")

type DraftService struct {
	db *database.DB
}

func NewDraftService(db *database.DB) *DraftService {
	return &DraftService{db: db}
}

func (s *DraftService) List(ctx context.Context, userID primitive.ObjectID) ([]models.Draft, error) {
	opts := options.Find().SetSort(bson.D{{Key: "updated_at", Value: -1}})
	cur, err := s.db.Col(database.ColDrafts).Find(ctx, bson.M{"user_id": userID}, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []models.Draft{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *DraftService) Get(ctx context.Context, userID, id primitive.ObjectID) (*models.Draft, error) {
	var d models.Draft
	err := s.db.Col(database.ColDrafts).FindOne(ctx, bson.M{"_id": id, "user_id": userID}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrDraftNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *DraftService) Create(ctx context.Context, userID primitive.ObjectID, req models.DraftRequest) (*models.Draft, error) {
	accID, err := primitive.ObjectIDFromHex(req.AccountID)
	if err != nil {
		return nil, errors.New("invalid account_id")
	}
	now := time.Now()
	d := models.Draft{
		UserID: userID, AccountID: accID,
		To: req.To, Cc: req.Cc, Bcc: req.Bcc, Subject: req.Subject, HTML: req.HTML,
		SignatureID: req.SignatureID, InReplyTo: req.InReplyTo, References: req.References,
		CreatedAt: now, UpdatedAt: now,
	}
	res, err := s.db.Col(database.ColDrafts).InsertOne(ctx, d)
	if err != nil {
		return nil, err
	}
	d.ID = res.InsertedID.(primitive.ObjectID)
	return &d, nil
}

func (s *DraftService) Update(ctx context.Context, userID, id primitive.ObjectID, req models.DraftRequest) (*models.Draft, error) {
	accID, err := primitive.ObjectIDFromHex(req.AccountID)
	if err != nil {
		return nil, errors.New("invalid account_id")
	}
	res, err := s.db.Col(database.ColDrafts).UpdateOne(ctx,
		bson.M{"_id": id, "user_id": userID},
		bson.M{"$set": bson.M{
			"account_id": accID, "to": req.To, "cc": req.Cc, "bcc": req.Bcc,
			"subject": req.Subject, "html": req.HTML, "signature_id": req.SignatureID,
			"in_reply_to": req.InReplyTo, "references": req.References, "updated_at": time.Now(),
		}})
	if err != nil {
		return nil, err
	}
	if res.MatchedCount == 0 {
		return nil, ErrDraftNotFound
	}
	return s.Get(ctx, userID, id)
}

func (s *DraftService) Delete(ctx context.Context, userID, id primitive.ObjectID) error {
	res, err := s.db.Col(database.ColDrafts).DeleteOne(ctx, bson.M{"_id": id, "user_id": userID})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrDraftNotFound
	}
	return nil
}
