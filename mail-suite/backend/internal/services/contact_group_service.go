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

var ErrGroupNotFound = errors.New("group not found")

type ContactGroupService struct {
	db *database.DB
}

func NewContactGroupService(db *database.DB) *ContactGroupService {
	return &ContactGroupService{db: db}
}

func (s *ContactGroupService) List(ctx context.Context, userID primitive.ObjectID) ([]models.ContactGroup, error) {
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	cur, err := s.db.Col(database.ColContactGroups).Find(ctx, bson.M{"user_id": userID}, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []models.ContactGroup{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *ContactGroupService) Get(ctx context.Context, userID, id primitive.ObjectID) (*models.ContactGroup, error) {
	var g models.ContactGroup
	err := s.db.Col(database.ColContactGroups).FindOne(ctx, bson.M{"_id": id, "user_id": userID}).Decode(&g)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrGroupNotFound
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *ContactGroupService) Create(ctx context.Context, userID primitive.ObjectID, req models.ContactGroupRequest) (*models.ContactGroup, error) {
	now := time.Now()
	g := models.ContactGroup{
		UserID: userID, Name: req.Name, Description: req.Description, Color: req.Color,
		ContactCount: 0, CreatedAt: now, UpdatedAt: now,
	}
	res, err := s.db.Col(database.ColContactGroups).InsertOne(ctx, g)
	if err != nil {
		return nil, err
	}
	g.ID = res.InsertedID.(primitive.ObjectID)
	return &g, nil
}

func (s *ContactGroupService) Update(ctx context.Context, userID, id primitive.ObjectID, req models.ContactGroupRequest) (*models.ContactGroup, error) {
	res, err := s.db.Col(database.ColContactGroups).UpdateOne(ctx,
		bson.M{"_id": id, "user_id": userID},
		bson.M{"$set": bson.M{"name": req.Name, "description": req.Description, "color": req.Color, "updated_at": time.Now()}})
	if err != nil {
		return nil, err
	}
	if res.MatchedCount == 0 {
		return nil, ErrGroupNotFound
	}
	return s.Get(ctx, userID, id)
}

// Delete removes the group and pulls its id out of every contact's membership.
func (s *ContactGroupService) Delete(ctx context.Context, userID, id primitive.ObjectID) error {
	res, err := s.db.Col(database.ColContactGroups).DeleteOne(ctx, bson.M{"_id": id, "user_id": userID})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrGroupNotFound
	}
	_, _ = s.db.Col(database.ColContacts).UpdateMany(ctx,
		bson.M{"user_id": userID, "group_ids": id},
		bson.M{"$pull": bson.M{"group_ids": id}})
	return nil
}

// RecomputeCounts refreshes contact_count for the given groups. Best-effort.
func (s *ContactGroupService) RecomputeCounts(ctx context.Context, userID primitive.ObjectID, groupIDs []primitive.ObjectID) {
	seen := map[primitive.ObjectID]bool{}
	for _, gid := range groupIDs {
		if gid.IsZero() || seen[gid] {
			continue
		}
		seen[gid] = true
		n, err := s.db.Col(database.ColContacts).CountDocuments(ctx, bson.M{"user_id": userID, "group_ids": gid})
		if err != nil {
			continue
		}
		_, _ = s.db.Col(database.ColContactGroups).UpdateOne(ctx,
			bson.M{"_id": gid, "user_id": userID},
			bson.M{"$set": bson.M{"contact_count": n, "updated_at": time.Now()}})
	}
}
