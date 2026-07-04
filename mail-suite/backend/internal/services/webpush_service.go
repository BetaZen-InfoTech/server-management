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
	s := &WebPushService{db: db, pub: cfg.VapidPublic, priv: cfg.VapidPrivate, subject: cfg.VapidSubject}
	// When no keypair is configured, self-provision one: load a previously
	// generated pair from Mongo, or generate + persist a fresh one. This makes
	// push work out of the box on every server without ever committing a key to
	// the repo, and keeps the pair stable across restarts (existing browser
	// subscriptions stay valid).
	if s.pub == "" || s.priv == "" {
		s.pub, s.priv = loadOrCreateVAPID(db)
	}
	return s
}

// loadOrCreateVAPID returns the server's persisted VAPID pair, generating and
// storing one on first call. Concurrent callers converge on the same stored
// pair (the doc is keyed on a fixed _id, so a racing insert loses and we
// re-read the winner).
func loadOrCreateVAPID(db *database.DB) (pub, priv string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	col := db.Col(database.ColSettings)
	var doc struct {
		Public  string `bson:"public"`
		Private string `bson:"private"`
	}
	if err := col.FindOne(ctx, bson.M{"_id": "vapid"}).Decode(&doc); err == nil && doc.Public != "" && doc.Private != "" {
		return doc.Public, doc.Private
	}
	newPriv, newPub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		log.Error().Err(err).Msg("generate VAPID keys — push disabled")
		return "", ""
	}
	// setOnInsert so the first writer wins and later writers are no-ops.
	_, _ = col.UpdateOne(ctx, bson.M{"_id": "vapid"},
		bson.M{"$setOnInsert": bson.M{"public": newPub, "private": newPriv, "created_at": time.Now()}},
		options.Update().SetUpsert(true))
	// Re-read to return whichever pair actually landed (handles the race).
	if err := col.FindOne(ctx, bson.M{"_id": "vapid"}).Decode(&doc); err == nil && doc.Public != "" {
		log.Info().Msg("web push: using auto-generated VAPID keypair")
		return doc.Public, doc.Private
	}
	return newPub, newPriv
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

// SendToUser delivers a payload to every browser the user has subscribed and
// returns how many were actually accepted by the push service. Dead
// subscriptions (404/410) are pruned. Failures are logged, never returned — a
// notification is best-effort.
func (s *WebPushService) SendToUser(ctx context.Context, userID primitive.ObjectID, p PushPayload) int {
	if !s.Enabled() {
		return 0
	}
	subs, err := s.ListByUser(ctx, userID)
	if err != nil || len(subs) == 0 {
		return 0
	}
	body, err := json.Marshal(p)
	if err != nil {
		return 0
	}
	delivered := 0
	for i := range subs {
		if s.sendOne(ctx, &subs[i], body) {
			delivered++
		}
	}
	return delivered
}

// sendOne pushes to a single subscription and reports whether the push service
// accepted it (2xx/3xx). A 404/410 endpoint is pruned.
func (s *WebPushService) sendOne(ctx context.Context, sub *models.PushSubscription, body []byte) bool {
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
		return false
	}
	defer resp.Body.Close()
	// 404/410 = the browser unsubscribed / the endpoint is gone — prune it so we
	// stop trying.
	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		_, _ = s.db.Col(database.ColPushSubs).DeleteOne(ctx, bson.M{"_id": sub.ID})
		return false
	}
	if resp.StatusCode >= 400 {
		log.Warn().Int("status", resp.StatusCode).Str("endpoint", clip(sub.Endpoint, 40)).Msg("web push non-2xx")
		return false
	}
	return true
}
