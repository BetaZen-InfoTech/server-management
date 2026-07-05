package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/database"
	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// NotifyWorker polls the INBOX of every account whose owner has at least one
// Web Push subscription and fires a browser/PWA notification when new mail
// arrives. "New" is detected cheaply via IMAP STATUS UIDNEXT — we remember the
// last value per account (NotifyState) and notify when it advances. The first
// time we see an account we only record a baseline (no notification for the
// mail already sitting there).
type NotifyWorker struct {
	db   *database.DB
	push *WebPushService
	fcm  *FCMService
}

func NewNotifyWorker(db *database.DB, push *WebPushService, fcm *FCMService) *NotifyWorker {
	return &NotifyWorker{db: db, push: push, fcm: fcm}
}

func (w *NotifyWorker) Start(ctx context.Context) {
	if !w.push.Enabled() && !w.fcm.Enabled() {
		log.Info().Msg("notify worker disabled (no VAPID keys, no FCM credentials)")
		return
	}
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		w.tick(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				w.tick(ctx)
			}
		}
	}()
	log.Info().Msg("new-mail notify worker started")
}

func (w *NotifyWorker) tick(ctx context.Context) {
	// Poll accounts owned by users who can actually receive a notification —
	// i.e. they have a browser Web Push subscription OR a registered mobile
	// device (FCM). Union the two so a mobile-only user still gets alerted.
	userSet := map[primitive.ObjectID]bool{}
	if ids, err := w.db.Col(database.ColPushSubs).Distinct(ctx, "user_id", bson.M{}); err == nil {
		for _, id := range ids {
			if oid, ok := id.(primitive.ObjectID); ok {
				userSet[oid] = true
			}
		}
	}
	if ids, err := w.db.Col(database.ColDevices).Distinct(ctx, "user_id", bson.M{}); err == nil {
		for _, id := range ids {
			if oid, ok := id.(primitive.ObjectID); ok {
				userSet[oid] = true
			}
		}
	}
	if len(userSet) == 0 {
		return
	}
	userIDs := make([]primitive.ObjectID, 0, len(userSet))
	for id := range userSet {
		userIDs = append(userIDs, id)
	}
	cur, err := w.db.Col(database.ColAccounts).Find(ctx, bson.M{"user_id": bson.M{"$in": userIDs}})
	if err != nil {
		return
	}
	var accounts []models.MailAccount
	if err := cur.All(ctx, &accounts); err != nil {
		return
	}
	for i := range accounts {
		if ctx.Err() != nil {
			return
		}
		w.pollAccount(ctx, &accounts[i])
	}
}

func (w *NotifyWorker) pollAccount(ctx context.Context, a *models.MailAccount) {
	uidNext, unseen, err := MailboxStatus(a, "INBOX")
	if err != nil || uidNext == 0 {
		return // unreachable / auth issue — stay quiet, retry next tick
	}

	var st models.NotifyState
	err = w.db.Col(database.ColNotifyState).FindOne(ctx, bson.M{"account_id": a.ID}).Decode(&st)
	// Only "no document" means first observation. A REAL read error must abort
	// the whole tick — advancing the watermark on a transient DB error would
	// permanently swallow the notification for whatever mail just arrived.
	baseline := errors.Is(err, mongo.ErrNoDocuments)
	if err != nil && !baseline {
		return
	}

	// Persist the new high-water mark regardless of whether we notify.
	defer func() {
		_, _ = w.db.Col(database.ColNotifyState).UpdateOne(ctx,
			bson.M{"account_id": a.ID},
			bson.M{"$set": bson.M{
				"account_id":   a.ID,
				"user_id":      a.UserID,
				"last_uidnext": uidNext,
				"updated_at":   time.Now(),
			}},
			options.Update().SetUpsert(true))
	}()

	// Notify only on a genuine advance past a known baseline (guards first-run
	// and UIDVALIDITY resets, which would make uidNext jump backwards).
	if baseline || uidNext <= st.LastUIDNext {
		return
	}
	newCount := int(uidNext - st.LastUIDNext)
	w.notifyNewMail(ctx, a, newCount, int(unseen))
}

func (w *NotifyWorker) notifyNewMail(ctx context.Context, a *models.MailAccount, newCount, unseen int) {
	title := "New mail"
	if a.Address != "" {
		title = "New mail — " + a.Address
	}
	body := fmt.Sprintf("%d new message%s", newCount, plural(newCount))

	// Best-effort: enrich with the newest message's sender + subject.
	if hdrs, _, err := ListHeaders(a, "INBOX", 1, 1); err == nil && len(hdrs) > 0 {
		h := hdrs[0]
		from := "(unknown sender)"
		if len(h.From) > 0 {
			if h.From[0].Name != "" {
				from = h.From[0].Name
			} else if h.From[0].Address != "" {
				from = h.From[0].Address
			}
		}
		subj := h.Subject
		if subj == "" {
			subj = "(no subject)"
		}
		body = from + " — " + subj
		if newCount > 1 {
			body += fmt.Sprintf("  (+%d more)", newCount-1)
		}
	}

	webN := w.push.SendToUser(ctx, a.UserID, PushPayload{
		Title: title,
		Body:  body,
		URL:   "/mail/inbox",
		Tag:   "new-mail-" + a.ID.Hex(),
	})
	fcmN := w.fcm.SendToUser(ctx, a.UserID, title, body, "/mail/inbox")
	log.Info().Str("account", a.Address).Int("new", newCount).Int("web", webN).Int("fcm", fcmN).Msg("new-mail push sent")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
