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

var (
	ErrCampaignNotFound = errors.New("campaign not found")
	ErrCampaignState    = errors.New("campaign is not in a state that allows this action")
	ErrNoRecipients     = errors.New("no subscribed contacts to send to")
)

type CampaignService struct {
	db *database.DB
}

func NewCampaignService(db *database.DB) *CampaignService {
	return &CampaignService{db: db}
}

func (s *CampaignService) List(ctx context.Context, userID primitive.ObjectID) ([]models.Campaign, error) {
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	cur, err := s.db.Col(database.ColCampaigns).Find(ctx, bson.M{"user_id": userID}, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []models.Campaign{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *CampaignService) Get(ctx context.Context, userID, id primitive.ObjectID) (*models.Campaign, error) {
	var c models.Campaign
	err := s.db.Col(database.ColCampaigns).FindOne(ctx, bson.M{"_id": id, "user_id": userID}).Decode(&c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrCampaignNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *CampaignService) Create(ctx context.Context, userID primitive.ObjectID, req models.CampaignRequest) (*models.Campaign, error) {
	accID, err := primitive.ObjectIDFromHex(req.AccountID)
	if err != nil {
		return nil, errors.New("invalid account_id")
	}
	if !req.AllContacts && len(req.GroupIDs) == 0 {
		return nil, errors.New("select at least one group, or turn on send to all contacts")
	}
	now := time.Now()
	c := models.Campaign{
		UserID: userID, AccountID: accID, Name: req.Name, Subject: req.Subject, HTML: req.HTML,
		GroupIDs: toOIDs(req.GroupIDs), AllContacts: req.AllContacts, Mode: normalizeMode(req.Mode),
		BatchSize: clampBatch(req.BatchSize), IntervalSeconds: clampInterval(req.IntervalSeconds),
		Status: models.CampaignDraft, CreatedAt: now, UpdatedAt: now,
	}
	if sid, err := primitive.ObjectIDFromHex(req.SignatureID); err == nil {
		c.SignatureID = sid
	}
	if c.GroupIDs == nil {
		c.GroupIDs = []primitive.ObjectID{}
	}
	res, err := s.db.Col(database.ColCampaigns).InsertOne(ctx, c)
	if err != nil {
		return nil, err
	}
	c.ID = res.InsertedID.(primitive.ObjectID)
	return &c, nil
}

// Update edits a campaign — only allowed while it's still a draft.
func (s *CampaignService) Update(ctx context.Context, userID, id primitive.ObjectID, req models.CampaignRequest) (*models.Campaign, error) {
	c, err := s.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if c.Status != models.CampaignDraft {
		return nil, ErrCampaignState
	}
	accID, err := primitive.ObjectIDFromHex(req.AccountID)
	if err != nil {
		return nil, errors.New("invalid account_id")
	}
	if !req.AllContacts && len(req.GroupIDs) == 0 {
		return nil, errors.New("select at least one group, or turn on send to all contacts")
	}
	set := bson.M{
		"account_id": accID, "name": req.Name, "subject": req.Subject, "html": req.HTML,
		"group_ids": toOIDs(req.GroupIDs), "all_contacts": req.AllContacts, "mode": normalizeMode(req.Mode),
		"batch_size": clampBatch(req.BatchSize), "interval_seconds": clampInterval(req.IntervalSeconds),
		"updated_at": time.Now(),
	}
	if sid, err := primitive.ObjectIDFromHex(req.SignatureID); err == nil {
		set["signature_id"] = sid
	}
	if _, err := s.db.Col(database.ColCampaigns).UpdateOne(ctx, bson.M{"_id": id, "user_id": userID}, bson.M{"$set": set}); err != nil {
		return nil, err
	}
	return s.Get(ctx, userID, id)
}

// Start materializes recipients (subscribed contacts in the target groups) and
// flips the campaign to "sending" so the worker picks it up. Resumes a paused
// campaign without regenerating recipients.
func (s *CampaignService) Start(ctx context.Context, userID, id primitive.ObjectID) error {
	c, err := s.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	now := time.Now()

	if c.Status == models.CampaignPaused {
		_, err = s.db.Col(database.ColCampaigns).UpdateOne(ctx, bson.M{"_id": id, "user_id": userID},
			bson.M{"$set": bson.M{"status": models.CampaignSending, "next_run_at": now, "updated_at": now}})
		return err
	}
	if c.Status != models.CampaignDraft {
		return ErrCampaignState
	}

	// Atomically claim the draft so two concurrent starts (e.g. a double-clicked
	// Send) can't both materialize recipients and email every contact twice. Only
	// the update whose filter still matches status=draft wins; the loser gets
	// MatchedCount==0. next_run_at is parked in the future so the worker doesn't
	// pick up this now-"sending" campaign before its recipients exist.
	claim, err := s.db.Col(database.ColCampaigns).UpdateOne(ctx,
		bson.M{"_id": id, "user_id": userID, "status": models.CampaignDraft},
		bson.M{"$set": bson.M{"status": models.CampaignSending, "started_at": now, "next_run_at": now.Add(time.Hour), "updated_at": now}})
	if err != nil {
		return err
	}
	if claim.MatchedCount == 0 {
		return ErrCampaignState // lost the race, or no longer a draft
	}

	// Materialize recipients: every subscribed contact (AllContacts) or those in
	// any target group, deduped (each contact doc appears once for a $in over its
	// group_ids).
	filter := bson.M{"user_id": userID, "status": models.ContactSubscribed}
	if !c.AllContacts {
		filter["group_ids"] = bson.M{"$in": c.GroupIDs}
	}
	cur, err := s.db.Col(database.ColContacts).Find(ctx, filter)
	if err != nil {
		return err
	}
	defer cur.Close(ctx)

	docs := []interface{}{}
	for cur.Next(ctx) {
		var ct models.Contact
		if err := cur.Decode(&ct); err != nil {
			continue
		}
		docs = append(docs, models.CampaignRecipient{
			CampaignID: c.ID, UserID: userID, AccountID: c.AccountID, ContactID: ct.ID,
			Email: ct.Email, Name: ct.Name, Fields: ct.Fields,
			TrackID: newToken(16), UnsubToken: ct.UnsubToken,
			Status: models.RecipientPending, CreatedAt: now,
		})
	}
	if len(docs) == 0 {
		// Nothing to send — release the claim back to draft so the user can fix
		// targeting and retry.
		_, _ = s.db.Col(database.ColCampaigns).UpdateOne(ctx, bson.M{"_id": id, "user_id": userID},
			bson.M{"$set": bson.M{"status": models.CampaignDraft, "next_run_at": nil, "started_at": nil, "updated_at": now}})
		return ErrNoRecipients
	}
	// Insert in chunks so a huge list doesn't build one giant BSON array.
	for i := 0; i < len(docs); i += 1000 {
		end := i + 1000
		if end > len(docs) {
			end = len(docs)
		}
		if _, err := s.db.Col(database.ColCampaignRecipients).InsertMany(ctx, docs[i:end]); err != nil {
			return err
		}
	}
	// Release the parked claim: publish the count and make it due now so the
	// worker starts sending on its next tick.
	_, err = s.db.Col(database.ColCampaigns).UpdateOne(ctx, bson.M{"_id": id, "user_id": userID},
		bson.M{"$set": bson.M{
			"total_recipients": len(docs), "next_run_at": now, "updated_at": now,
		}})
	return err
}

func (s *CampaignService) setStatus(ctx context.Context, userID, id primitive.ObjectID, from []string, to string) error {
	c, err := s.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	ok := false
	for _, f := range from {
		if c.Status == f {
			ok = true
		}
	}
	if !ok {
		return ErrCampaignState
	}
	_, err = s.db.Col(database.ColCampaigns).UpdateOne(ctx, bson.M{"_id": id, "user_id": userID},
		bson.M{"$set": bson.M{"status": to, "updated_at": time.Now()}})
	return err
}

func (s *CampaignService) Pause(ctx context.Context, userID, id primitive.ObjectID) error {
	return s.setStatus(ctx, userID, id, []string{models.CampaignSending}, models.CampaignPaused)
}

func (s *CampaignService) Cancel(ctx context.Context, userID, id primitive.ObjectID) error {
	return s.setStatus(ctx, userID, id, []string{models.CampaignSending, models.CampaignPaused, models.CampaignDraft}, models.CampaignCanceled)
}

// Delete removes a campaign and all its recipient rows.
func (s *CampaignService) Delete(ctx context.Context, userID, id primitive.ObjectID) error {
	res, err := s.db.Col(database.ColCampaigns).DeleteOne(ctx, bson.M{"_id": id, "user_id": userID})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrCampaignNotFound
	}
	_, _ = s.db.Col(database.ColCampaignRecipients).DeleteMany(ctx, bson.M{"campaign_id": id, "user_id": userID})
	return nil
}

// Stats returns the live rollup for a campaign (owner-scoped).
func (s *CampaignService) Stats(ctx context.Context, userID, id primitive.ObjectID) (*models.CampaignStats, error) {
	if _, err := s.Get(ctx, userID, id); err != nil {
		return nil, err
	}
	col := s.db.Col(database.ColCampaignRecipients)
	count := func(extra bson.M) int {
		f := bson.M{"campaign_id": id}
		for k, v := range extra {
			f[k] = v
		}
		n, _ := col.CountDocuments(ctx, f)
		return int(n)
	}
	st := &models.CampaignStats{
		Total:        count(nil),
		Pending:      count(bson.M{"status": models.RecipientPending}),
		Sent:         count(bson.M{"status": models.RecipientSent}),
		Failed:       count(bson.M{"status": models.RecipientFailed}),
		Bounced:      count(bson.M{"status": models.RecipientBounced}),
		Unsubscribed: count(bson.M{"status": models.RecipientUnsubscribed}),
		Opened:       count(bson.M{"open_count": bson.M{"$gt": 0}}),
		Clicked:      count(bson.M{"click_count": bson.M{"$gt": 0}}),
	}
	// open/click totals via aggregation
	cur, err := col.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"campaign_id": id}}},
		{{Key: "$group", Value: bson.M{"_id": nil,
			"opens":  bson.M{"$sum": "$open_count"},
			"clicks": bson.M{"$sum": "$click_count"}}}},
	})
	if err == nil {
		defer cur.Close(ctx)
		if cur.Next(ctx) {
			var r struct {
				Opens  int `bson:"opens"`
				Clicks int `bson:"clicks"`
			}
			if cur.Decode(&r) == nil {
				st.OpenTotal = r.Opens
				st.ClickTotal = r.Clicks
			}
		}
	}
	return st, nil
}

// Recipients lists a page of a campaign's recipients (newest activity first).
func (s *CampaignService) Recipients(ctx context.Context, userID, id primitive.ObjectID, page, limit int) ([]models.CampaignRecipient, error) {
	if _, err := s.Get(ctx, userID, id); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if page <= 0 {
		page = 1
	}
	opts := options.Find().SetSort(bson.D{{Key: "sent_at", Value: -1}, {Key: "_id", Value: 1}}).
		SetSkip(int64((page - 1) * limit)).SetLimit(int64(limit))
	cur, err := s.db.Col(database.ColCampaignRecipients).Find(ctx, bson.M{"campaign_id": id}, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []models.CampaignRecipient{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RecipientEvents returns the full open/click event timeline for one recipient
// (owner-scoped via the campaign), oldest first — powers the per-recipient
// analytics drill-down (which link, when, how many).
func (s *CampaignService) RecipientEvents(ctx context.Context, userID, campaignID, recipientID primitive.ObjectID) ([]models.TrackingEvent, error) {
	if _, err := s.Get(ctx, userID, campaignID); err != nil {
		return nil, err
	}
	var r models.CampaignRecipient
	err := s.db.Col(database.ColCampaignRecipients).FindOne(ctx, bson.M{"_id": recipientID, "campaign_id": campaignID}).Decode(&r)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrCampaignNotFound
	}
	if err != nil {
		return nil, err
	}
	opts := options.Find().SetSort(bson.D{{Key: "at", Value: 1}}).SetLimit(1000)
	cur, err := s.db.Col(database.ColTracking).Find(ctx, bson.M{"track_id": r.TrackID}, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	events := []models.TrackingEvent{}
	if err := cur.All(ctx, &events); err != nil {
		return nil, err
	}
	return events, nil
}

func normalizeMode(m string) string {
	if m == models.CampaignModeDrip {
		return models.CampaignModeDrip
	}
	return models.CampaignModeNow
}

func clampBatch(n int) int {
	if n <= 0 {
		return 50
	}
	if n > 500 {
		return 500
	}
	return n
}

func clampInterval(n int) int {
	if n < 0 {
		return 0
	}
	if n > 86400 {
		return 86400
	}
	return n
}
