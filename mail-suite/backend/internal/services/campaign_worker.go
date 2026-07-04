package services

import (
	"context"
	"strings"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/config"
	"github.com/betazeninfotech/mail-suite/internal/database"
	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// CampaignWorker is the single background goroutine that drives campaign
// sending. Every tick it finds "sending" campaigns whose next_run_at is due and
// sends one batch of pending recipients each — send-now (re-runs immediately)
// and drip (waits IntervalSeconds between batches) share this path. Ticks never
// overlap (the loop waits for tick() to finish), and on boot the first tick
// resumes any campaign left "sending" after a restart.
type CampaignWorker struct {
	db       *database.DB
	accounts *AccountService
	cfg      *config.Config
}

func NewCampaignWorker(db *database.DB, accounts *AccountService, cfg *config.Config) *CampaignWorker {
	return &CampaignWorker{db: db, accounts: accounts, cfg: cfg}
}

func (w *CampaignWorker) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		w.tick(ctx) // boot recovery
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				w.tick(ctx)
			}
		}
	}()
	log.Info().Msg("campaign worker started")
}

func (w *CampaignWorker) tick(ctx context.Context) {
	now := time.Now()
	cur, err := w.db.Col(database.ColCampaigns).Find(ctx, bson.M{
		"status": models.CampaignSending,
		"$or": bson.A{
			bson.M{"next_run_at": bson.M{"$lte": now}},
			bson.M{"next_run_at": bson.M{"$exists": false}},
			bson.M{"next_run_at": nil},
		},
	})
	if err != nil {
		return
	}
	var camps []models.Campaign
	if err := cur.All(ctx, &camps); err != nil {
		return
	}
	for i := range camps {
		if ctx.Err() != nil {
			return
		}
		w.processCampaign(ctx, &camps[i])
	}
}

func (w *CampaignWorker) processCampaign(ctx context.Context, c *models.Campaign) {
	acc, err := w.accounts.Get(ctx, c.UserID, c.AccountID)
	if err != nil {
		w.failCampaign(ctx, c.ID, "sending mailbox unavailable: "+err.Error())
		return
	}
	sigHTML := w.signatureHTML(ctx, c.UserID, c.SignatureID)
	tr := acc.EffectiveTracking()
	baseURL := w.cfg.PublicURL
	batch := c.BatchSize
	if batch <= 0 {
		batch = 50
	}

	// Claim a batch of pending recipients.
	opts := options.Find().SetLimit(int64(batch)).SetSort(bson.D{{Key: "_id", Value: 1}})
	cur, err := w.db.Col(database.ColCampaignRecipients).Find(ctx, bson.M{"campaign_id": c.ID, "status": models.RecipientPending}, opts)
	if err != nil {
		return
	}
	var recips []models.CampaignRecipient
	if err := cur.All(ctx, &recips); err != nil {
		return
	}

	if len(recips) == 0 {
		// Nothing left — campaign is complete.
		now := time.Now()
		_, _ = w.db.Col(database.ColCampaigns).UpdateOne(ctx, bson.M{"_id": c.ID},
			bson.M{"$set": bson.M{"status": models.CampaignSent, "completed_at": now, "next_run_at": nil, "updated_at": now}})
		log.Info().Str("campaign", c.Name).Int("sent", c.SentCount).Msg("campaign complete")
		return
	}

	sent, failed := 0, 0
	for i := range recips {
		if ctx.Err() != nil {
			break
		}
		// Respect a pause/cancel that happened mid-batch.
		if !w.stillSending(ctx, c.ID) {
			break
		}
		r := &recips[i]
		now := time.Now()

		// Never mail a contact who has since unsubscribed / bounced.
		if !w.contactSubscribed(ctx, r.ContactID) {
			_, _ = w.db.Col(database.ColCampaignRecipients).UpdateOne(ctx, bson.M{"_id": r.ID},
				bson.M{"$set": bson.M{"status": models.RecipientUnsubscribed}})
			continue
		}

		html := applyMerge(c.HTML, r)
		html = applySignature(html, sigHTML)
		unsubURL := strings.TrimRight(baseURL, "/") + "/u/" + r.UnsubToken
		html += unsubFooter(unsubURL)
		if tr.Click {
			html = rewriteLinks(html, baseURL, r.TrackID, w.cfg.JWTSecret)
		}
		if tr.Open {
			html = injectOpenPixel(html, baseURL, r.TrackID)
		}
		text := htmlToText(html)
		messageID := buildMessageID(acc.Address)

		req := &models.SendRequest{
			To:      []models.Address{{Name: r.Name, Address: r.Email}},
			Subject: applyMerge(c.Subject, r),
			HTML:    html, Text: text,
		}
		headers := map[string]string{
			"List-Unsubscribe":      "<" + unsubURL + ">",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
			"Precedence":            "bulk",
		}
		buf, berr := buildMIME(acc, req, html, text, messageID, headers)
		if berr == nil {
			berr = smtpSend(acc, []string{r.Email}, buf)
		}
		if berr != nil {
			_, _ = w.db.Col(database.ColCampaignRecipients).UpdateOne(ctx, bson.M{"_id": r.ID},
				bson.M{"$set": bson.M{"status": models.RecipientFailed, "error": clip(berr.Error(), 300)}})
			failed++
		} else {
			_, _ = w.db.Col(database.ColCampaignRecipients).UpdateOne(ctx, bson.M{"_id": r.ID},
				bson.M{"$set": bson.M{"status": models.RecipientSent, "sent_at": now, "message_id": messageID}})
			sent++
		}
		// Gentle pacing so we don't hammer the SMTP server within a batch.
		select {
		case <-ctx.Done():
		case <-time.After(150 * time.Millisecond):
		}
	}

	// Roll up counters and schedule the next batch.
	now := time.Now()
	set := bson.M{"updated_at": now}
	if c.Mode == models.CampaignModeDrip && c.IntervalSeconds > 0 {
		next := now.Add(time.Duration(c.IntervalSeconds) * time.Second)
		set["next_run_at"] = next
	} else {
		set["next_run_at"] = now // send-now: continue next tick
	}
	_, _ = w.db.Col(database.ColCampaigns).UpdateOne(ctx, bson.M{"_id": c.ID},
		bson.M{"$inc": bson.M{"sent_count": sent, "failed_count": failed}, "$set": set})
}

func (w *CampaignWorker) stillSending(ctx context.Context, id primitive.ObjectID) bool {
	var c models.Campaign
	if err := w.db.Col(database.ColCampaigns).FindOne(ctx, bson.M{"_id": id}, options.FindOne().SetProjection(bson.M{"status": 1})).Decode(&c); err != nil {
		return false
	}
	return c.Status == models.CampaignSending
}

func (w *CampaignWorker) contactSubscribed(ctx context.Context, contactID primitive.ObjectID) bool {
	var ct models.Contact
	if err := w.db.Col(database.ColContacts).FindOne(ctx, bson.M{"_id": contactID}, options.FindOne().SetProjection(bson.M{"status": 1})).Decode(&ct); err != nil {
		return false // contact deleted → don't send
	}
	return ct.Status == models.ContactSubscribed
}

func (w *CampaignWorker) failCampaign(ctx context.Context, id primitive.ObjectID, reason string) {
	_, _ = w.db.Col(database.ColCampaigns).UpdateOne(ctx, bson.M{"_id": id},
		bson.M{"$set": bson.M{"status": models.CampaignFailed, "next_run_at": nil, "updated_at": time.Now()}})
	log.Warn().Str("campaign", id.Hex()).Msg("campaign failed: " + reason)
}

func (w *CampaignWorker) signatureHTML(ctx context.Context, userID, sigID primitive.ObjectID) string {
	if sigID.IsZero() {
		return ""
	}
	var sig models.Signature
	if err := w.db.Col(database.ColSignatures).FindOne(ctx, bson.M{"_id": sigID, "user_id": userID}).Decode(&sig); err == nil {
		return sig.HTML
	}
	return ""
}

// applyMerge substitutes {{name}}, {{email}}, {{first_name}} and any custom
// {{field_key}} in a template with the recipient's values.
func applyMerge(tmpl string, r *models.CampaignRecipient) string {
	first := r.Name
	if i := strings.IndexByte(first, ' '); i > 0 {
		first = first[:i]
	}
	out := strings.ReplaceAll(tmpl, "{{name}}", r.Name)
	out = strings.ReplaceAll(out, "{{email}}", r.Email)
	out = strings.ReplaceAll(out, "{{first_name}}", first)
	for k, v := range r.Fields {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

func unsubFooter(url string) string {
	return `<div style="margin-top:24px;padding-top:12px;border-top:1px solid #e5e7eb;font-size:12px;color:#94a3b8">` +
		`You received this because you're on our contact list. ` +
		`<a href="` + url + `" style="color:#64748b">Unsubscribe</a>.</div>`
}
