package services

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/database"
	"github.com/betazeninfotech/mail-suite/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var ErrSentNotFound = errors.New("sent message not found")

type TrackingService struct {
	db     *database.DB
	secret string // HMAC key for verifying click targets (== JWT secret)
}

func NewTrackingService(db *database.DB, secret string) *TrackingService {
	return &TrackingService{db: db, secret: secret}
}

// RecordOpen logs an open event (fired by the tracking pixel) and updates the
// SentMessage open counters. All writes are best-effort — a tracking pixel must
// always return an image regardless of DB state.
func (s *TrackingService) RecordOpen(ctx context.Context, trackID, ip, ua string) {
	if trackID == "" {
		return
	}
	now := time.Now()
	_, _ = s.db.Col(database.ColTracking).InsertOne(ctx, models.TrackingEvent{
		TrackID: trackID, AccountID: s.accountFor(ctx, trackID), Type: "open",
		IP: ip, UserAgent: ua, At: now,
	})
	_, _ = s.db.Col(database.ColSent).UpdateOne(ctx,
		bson.M{"track_id": trackID},
		bson.M{"$inc": bson.M{"open_count": 1}, "$set": bson.M{"last_open_at": now}})
	// first_open_at only when not yet recorded
	_, _ = s.db.Col(database.ColSent).UpdateOne(ctx,
		bson.M{"track_id": trackID, "first_open_at": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"first_open_at": now}})
}

// RecordClick decodes "<sig>:<url>" from the base64url `u` param, verifies the
// HMAC signature (so we never redirect to an unsigned/attacker-supplied URL —
// closing the open-redirect hole), logs a click event, bumps the counter, and
// returns the target URL to redirect to ("" if missing/invalid).
func (s *TrackingService) RecordClick(ctx context.Context, trackID, encodedURL, ip, ua string) string {
	target := ""
	if b, err := base64.RawURLEncoding.DecodeString(encodedURL); err == nil {
		payload := string(b)
		if i := strings.IndexByte(payload, ':'); i > 0 {
			sig, url := payload[:i], payload[i+1:]
			if verifyClick(s.secret, trackID, url, sig) {
				target = url
			}
		}
	}
	if target == "" {
		return ""
	}
	if trackID != "" {
		now := time.Now()
		_, _ = s.db.Col(database.ColTracking).InsertOne(ctx, models.TrackingEvent{
			TrackID: trackID, AccountID: s.accountFor(ctx, trackID), Type: "click",
			URL: target, IP: ip, UserAgent: ua, At: now,
		})
		_, _ = s.db.Col(database.ColSent).UpdateOne(ctx,
			bson.M{"track_id": trackID},
			bson.M{"$inc": bson.M{"click_count": 1}})
	}
	return target
}

func (s *TrackingService) accountFor(ctx context.Context, trackID string) primitive.ObjectID {
	var sent models.SentMessage
	if err := s.db.Col(database.ColSent).FindOne(ctx, bson.M{"track_id": trackID}).Decode(&sent); err == nil {
		return sent.AccountID
	}
	return primitive.NilObjectID
}

// ListSent returns the user's sent messages (optionally scoped to one mailbox),
// newest first — the data behind the "Sent + tracking" dashboard.
func (s *TrackingService) ListSent(ctx context.Context, userID, accountID primitive.ObjectID, limit int) ([]models.SentMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	filter := bson.M{"user_id": userID}
	if !accountID.IsZero() {
		filter["account_id"] = accountID
	}
	opts := options.Find().SetSort(bson.D{{Key: "sent_at", Value: -1}}).SetLimit(int64(limit))
	cur, err := s.db.Col(database.ColSent).Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []models.SentMessage{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Detail returns a single sent message (scoped to the owner) plus its events.
func (s *TrackingService) Detail(ctx context.Context, userID primitive.ObjectID, trackID string) (*models.SentMessage, []models.TrackingEvent, error) {
	var sent models.SentMessage
	err := s.db.Col(database.ColSent).FindOne(ctx, bson.M{"track_id": trackID, "user_id": userID}).Decode(&sent)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil, ErrSentNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	return s.withEvents(ctx, &sent)
}

// DetailByMessage finds the SentMessage for a given mailbox + RFC5322 Message-ID
// (tolerant of the surrounding <>) and returns it with its events. Powers the
// activity panel shown under a message in the Sent view.
func (s *TrackingService) DetailByMessage(ctx context.Context, userID, accountID primitive.ObjectID, messageID string) (*models.SentMessage, []models.TrackingEvent, error) {
	mid := strings.TrimSpace(messageID)
	stripped := strings.Trim(mid, "<>")
	var sent models.SentMessage
	err := s.db.Col(database.ColSent).FindOne(ctx, bson.M{
		"user_id":    userID,
		"account_id": accountID,
		"message_id": bson.M{"$in": bson.A{mid, "<" + stripped + ">", stripped}},
	}).Decode(&sent)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil, ErrSentNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	return s.withEvents(ctx, &sent)
}

// withEvents attaches the tracking events for a SentMessage, newest first.
func (s *TrackingService) withEvents(ctx context.Context, sent *models.SentMessage) (*models.SentMessage, []models.TrackingEvent, error) {
	opts := options.Find().SetSort(bson.D{{Key: "at", Value: -1}}).SetLimit(500)
	cur, err := s.db.Col(database.ColTracking).Find(ctx, bson.M{"track_id": sent.TrackID}, opts)
	if err != nil {
		return sent, nil, err
	}
	defer cur.Close(ctx)
	events := []models.TrackingEvent{}
	_ = cur.All(ctx, &events)
	return sent, events, nil
}
