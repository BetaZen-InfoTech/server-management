package services

import (
	"context"
	"encoding/json"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/betazeninfotech/mail-suite/internal/config"
	"github.com/betazeninfotech/mail-suite/internal/database"
	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// WebPushService stores browser Web Push subscriptions and delivers
// notifications to them via the VAPID/Web Push protocol. It is a no-op sender
// when the VAPID keys aren't configured (Enabled() == false) so the rest of the
// app runs fine without push set up.
type WebPushService struct {
	db      *database.DB
	pub     string
	priv    string
	subject string
}

func NewWebPushService(db *database.DB, cfg *config.Config) *WebPushService {
	return &WebPushService{db: db, pub: cfg.VapidPublic, priv: cfg.VapidPrivate, subject: cfg.VapidSubject}
}

func (s *WebPushService) Enabled() bool      { return s.pub != "" && s.priv != "" }
func (s *WebPushService) VapidPublic() string { return s.pub }

// PushPayload is the JSON body the service worker receives in its 'push' event.
type PushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"`
	Tag   string `json:"tag,omitempty"`
	Icon  string `json:"icon,omitempty"`
}

// Subscribe upserts a browser subscription by its (globally unique) endpoint.
func (s *WebPushService) Subscribe(ctx context.Context, userID primitive.ObjectID, req models.PushSubscribeRequest, ua string) error {
	now := time.Now()
	_, err := s.db.Col(database.ColPushSubs).UpdateOne(ctx,
		bson.M{"endpoint": req.Endpoint},
		bson.M{
			"$set": bson.M{
				"user_id":  userID,
				"endpoint": req.Endpoint,
				"p256dh":   req.Keys.P256dh,
				"auth":     req.Keys.Auth,
				"ua":       ua,
			},
			"$setOnInsert": bson.M{"created_at": now},
		},
		options.Update().SetUpsert(true))
	return err
}

func (s *WebPushService) Unsubscribe(ctx context.Context, userID primitive.ObjectID, endpoint string) error {
	_, err := s.db.Col(database.ColPushSubs).DeleteOne(ctx, bson.M{"user_id": userID, "endpoint": endpoint})
	return err
}

func (s *WebPushService) ListByUser(ctx context.Context, userID primitive.ObjectID) ([]models.PushSubscription, error) {
	cur, err := s.db.Col(database.ColPushSubs).Find(ctx, bson.M{"user_id": userID})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []models.PushSubscription{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SendToUser delivers a payload to every browser the user has subscribed. Dead
// subscriptions (the push service returns 404/410) are pruned. Failures are
// logged, never returned — a notification is best-effort.
func (s *WebPushService) SendToUser(ctx context.Context, userID primitive.ObjectID, p PushPayload) {
	if !s.Enabled() {
		return
	}
	subs, err := s.ListByUser(ctx, userID)
	if err != nil || len(subs) == 0 {
		return
	}
	body, err := json.Marshal(p)
	if err != nil {
		return
	}
	for i := range subs {
		s.sendOne(ctx, &subs[i], body)
	}
}

func (s *WebPushService) sendOne(ctx context.Context, sub *models.PushSubscription, body []byte) {
	resp, err := webpush.SendNotification(body, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		Subscriber:      s.subject,
		VAPIDPublicKey:  s.pub,
		VAPIDPrivateKey: s.priv,
		TTL:             120,
		Urgency:         webpush.UrgencyHigh,
	})
	if err != nil {
		log.Warn().Err(err).Str("endpoint", clip(sub.Endpoint, 40)).Msg("web push send failed")
		return
	}
	defer resp.Body.Close()
	// 404/410 = the browser unsubscribed / the endpoint is gone — prune it so we
	// stop trying.
	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		_, _ = s.db.Col(database.ColPushSubs).DeleteOne(ctx, bson.M{"_id": sub.ID})
		return
	}
	if resp.StatusCode >= 400 {
		log.Warn().Int("status", resp.StatusCode).Str("endpoint", clip(sub.Endpoint, 40)).Msg("web push non-2xx")
	}
}
