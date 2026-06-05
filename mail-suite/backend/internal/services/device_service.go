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

var ErrDeviceNotFound = errors.New("device not found")

type DeviceService struct {
	db *database.DB
}

func NewDeviceService(db *database.DB) *DeviceService {
	return &DeviceService{db: db}
}

func (s *DeviceService) Register(ctx context.Context, userID primitive.ObjectID, req models.RegisterDeviceRequest) (*models.Device, error) {
	now := time.Now()
	filter := bson.M{"user_id": userID, "fcm_token": req.FCMToken}
	update := bson.M{
		"$set": bson.M{
			"user_id": userID,
			"platform": req.Platform,
			"model": req.Model,
			"fcm_token": req.FCMToken,
			"app_ver": req.AppVer,
			"last_seen": now,
		},
		"$setOnInsert": bson.M{"created_at": now},
	}
	res, err := s.db.Col(database.ColDevices).UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
	if err != nil {
		return nil, err
	}
	var d models.Device
	if res.UpsertedID != nil {
		d.ID = res.UpsertedID.(primitive.ObjectID)
	} else {
		_ = s.db.Col(database.ColDevices).FindOne(ctx, filter).Decode(&d)
	}
	d.UserID = userID
	d.Platform = req.Platform
	d.Model = req.Model
	d.FCMToken = req.FCMToken
	d.AppVer = req.AppVer
	d.LastSeen = now
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	return &d, nil
}

func (s *DeviceService) Delete(ctx context.Context, userID, id primitive.ObjectID) error {
	res, err := s.db.Col(database.ColDevices).DeleteOne(ctx, bson.M{"_id": id, "user_id": userID})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

func (s *DeviceService) ListByUser(ctx context.Context, userID primitive.ObjectID) ([]models.Device, error) {
	cur, err := s.db.Col(database.ColDevices).Find(ctx, bson.M{"user_id": userID})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []models.Device{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}
