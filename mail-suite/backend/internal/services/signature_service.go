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
)

var ErrSignatureNotFound = errors.New("signature not found")

type SignatureService struct {
	db *database.DB
}

func NewSignatureService(db *database.DB) *SignatureService {
	return &SignatureService{db: db}
}

func (s *SignatureService) List(ctx context.Context, userID primitive.ObjectID) ([]models.Signature, error) {
	cur, err := s.db.Col(database.ColSignatures).Find(ctx, bson.M{"user_id": userID})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []models.Signature{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *SignatureService) Create(ctx context.Context, userID primitive.ObjectID, req models.SignatureRequest) (*models.Signature, error) {
	if req.IsDefault {
		_, _ = s.db.Col(database.ColSignatures).UpdateMany(ctx, bson.M{"user_id": userID}, bson.M{"$set": bson.M{"is_default": false}})
	}
	now := time.Now()
	sig := models.Signature{
		UserID: userID, Name: req.Name, HTML: req.HTML, IsDefault: req.IsDefault,
		CreatedAt: now, UpdatedAt: now,
	}
	res, err := s.db.Col(database.ColSignatures).InsertOne(ctx, sig)
	if err != nil {
		return nil, err
	}
	sig.ID = res.InsertedID.(primitive.ObjectID)
	return &sig, nil
}

func (s *SignatureService) Update(ctx context.Context, userID, id primitive.ObjectID, req models.SignatureRequest) (*models.Signature, error) {
	if req.IsDefault {
		_, _ = s.db.Col(database.ColSignatures).UpdateMany(ctx, bson.M{"user_id": userID, "_id": bson.M{"$ne": id}}, bson.M{"$set": bson.M{"is_default": false}})
	}
	res, err := s.db.Col(database.ColSignatures).UpdateOne(ctx,
		bson.M{"_id": id, "user_id": userID},
		bson.M{"$set": bson.M{
			"name": req.Name, "html": req.HTML, "is_default": req.IsDefault, "updated_at": time.Now(),
		}})
	if err != nil {
		return nil, err
	}
	if res.MatchedCount == 0 {
		return nil, ErrSignatureNotFound
	}
	var out models.Signature
	if err := s.db.Col(database.ColSignatures).FindOne(ctx, bson.M{"_id": id}).Decode(&out); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrSignatureNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (s *SignatureService) Delete(ctx context.Context, userID, id primitive.ObjectID) error {
	res, err := s.db.Col(database.ColSignatures).DeleteOne(ctx, bson.M{"_id": id, "user_id": userID})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrSignatureNotFound
	}
	return nil
}
