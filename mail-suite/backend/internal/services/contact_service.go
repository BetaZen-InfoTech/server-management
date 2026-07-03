package services

import (
	"context"
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

var (
	ErrContactNotFound = errors.New("contact not found")
	ErrContactExists   = errors.New("a contact with this email already exists")
)

type ContactService struct {
	db     *database.DB
	groups *ContactGroupService
}

func NewContactService(db *database.DB, groups *ContactGroupService) *ContactService {
	return &ContactService{db: db, groups: groups}
}

// toOIDs converts hex strings to ObjectIDs, silently dropping invalid ones.
func toOIDs(hexes []string) []primitive.ObjectID {
	out := make([]primitive.ObjectID, 0, len(hexes))
	for _, h := range hexes {
		if oid, err := primitive.ObjectIDFromHex(h); err == nil {
			out = append(out, oid)
		}
	}
	return out
}

func normEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// List returns a page of contacts, optionally filtered by group, status, and a
// case-insensitive search over email/name.
func (s *ContactService) List(ctx context.Context, userID primitive.ObjectID, groupID *primitive.ObjectID, status, search string, page, limit int) ([]models.Contact, int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if page <= 0 {
		page = 1
	}
	filter := bson.M{"user_id": userID}
	if groupID != nil {
		filter["group_ids"] = *groupID
	}
	if status != "" {
		filter["status"] = status
	}
	if q := strings.TrimSpace(search); q != "" {
		rx := primitive.Regex{Pattern: regexEscape(q), Options: "i"}
		filter["$or"] = bson.A{bson.M{"email": rx}, bson.M{"name": rx}}
	}
	total, err := s.db.Col(database.ColContacts).CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetSkip(int64((page - 1) * limit)).SetLimit(int64(limit))
	cur, err := s.db.Col(database.ColContacts).Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cur.Close(ctx)
	out := []models.Contact{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func (s *ContactService) Get(ctx context.Context, userID, id primitive.ObjectID) (*models.Contact, error) {
	var c models.Contact
	err := s.db.Col(database.ColContacts).FindOne(ctx, bson.M{"_id": id, "user_id": userID}).Decode(&c)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrContactNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *ContactService) Create(ctx context.Context, userID primitive.ObjectID, req models.ContactRequest) (*models.Contact, error) {
	status := req.Status
	if status == "" {
		status = models.ContactSubscribed
	}
	gids := toOIDs(req.GroupIDs)
	now := time.Now()
	c := models.Contact{
		UserID: userID, Email: normEmail(req.Email), Name: strings.TrimSpace(req.Name),
		Fields: req.Fields, GroupIDs: gids, Status: status, Source: "manual",
		UnsubToken: newToken(16), CreatedAt: now, UpdatedAt: now,
	}
	if c.GroupIDs == nil {
		c.GroupIDs = []primitive.ObjectID{}
	}
	res, err := s.db.Col(database.ColContacts).InsertOne(ctx, c)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, ErrContactExists
		}
		return nil, err
	}
	c.ID = res.InsertedID.(primitive.ObjectID)
	s.groups.RecomputeCounts(ctx, userID, gids)
	return &c, nil
}

func (s *ContactService) Update(ctx context.Context, userID, id primitive.ObjectID, req models.ContactRequest) (*models.Contact, error) {
	old, err := s.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	gids := toOIDs(req.GroupIDs)
	set := bson.M{
		"email": normEmail(req.Email), "name": strings.TrimSpace(req.Name),
		"fields": req.Fields, "group_ids": gids, "updated_at": time.Now(),
	}
	if req.Status != "" {
		set["status"] = req.Status
	}
	res, err := s.db.Col(database.ColContacts).UpdateOne(ctx, bson.M{"_id": id, "user_id": userID}, bson.M{"$set": set})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, ErrContactExists
		}
		return nil, err
	}
	if res.MatchedCount == 0 {
		return nil, ErrContactNotFound
	}
	s.groups.RecomputeCounts(ctx, userID, append(old.GroupIDs, gids...))
	return s.Get(ctx, userID, id)
}

func (s *ContactService) Delete(ctx context.Context, userID, id primitive.ObjectID) error {
	old, err := s.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	if _, err := s.db.Col(database.ColContacts).DeleteOne(ctx, bson.M{"_id": id, "user_id": userID}); err != nil {
		return err
	}
	s.groups.RecomputeCounts(ctx, userID, old.GroupIDs)
	return nil
}

// Import upserts a batch of contacts: existing emails gain the target groups and
// updated name/fields; new emails are created. Never duplicates an email.
func (s *ContactService) Import(ctx context.Context, userID primitive.ObjectID, req models.ContactImportRequest) (*models.ContactImportResult, error) {
	gids := toOIDs(req.GroupIDs)
	out := &models.ContactImportResult{}
	now := time.Now()
	for _, row := range req.Rows {
		email := normEmail(row.Email)
		if email == "" || !strings.Contains(email, "@") {
			out.Skipped++
			continue
		}
		var existing models.Contact
		err := s.db.Col(database.ColContacts).FindOne(ctx, bson.M{"user_id": userID, "email": email}).Decode(&existing)
		if err == nil {
			set := bson.M{"updated_at": now}
			if strings.TrimSpace(row.Name) != "" {
				set["name"] = strings.TrimSpace(row.Name)
			}
			for k, v := range row.Fields {
				set["fields."+k] = v
			}
			update := bson.M{"$set": set}
			if len(gids) > 0 {
				update["$addToSet"] = bson.M{"group_ids": bson.M{"$each": gids}}
			}
			if _, err := s.db.Col(database.ColContacts).UpdateOne(ctx, bson.M{"_id": existing.ID}, update); err != nil {
				out.Errors = append(out.Errors, email+": "+err.Error())
				continue
			}
			out.Updated++
			continue
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			out.Errors = append(out.Errors, email+": "+err.Error())
			continue
		}
		c := models.Contact{
			UserID: userID, Email: email, Name: strings.TrimSpace(row.Name), Fields: row.Fields,
			GroupIDs: gids, Status: models.ContactSubscribed, Source: "import",
			UnsubToken: newToken(16), CreatedAt: now, UpdatedAt: now,
		}
		if c.GroupIDs == nil {
			c.GroupIDs = []primitive.ObjectID{}
		}
		if _, err := s.db.Col(database.ColContacts).InsertOne(ctx, c); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				out.Skipped++
			} else {
				out.Errors = append(out.Errors, email+": "+err.Error())
			}
			continue
		}
		out.Created++
	}
	s.groups.RecomputeCounts(ctx, userID, gids)
	return out, nil
}

// SetStatus flips a contact's subscription status (used by the UI).
func (s *ContactService) SetStatus(ctx context.Context, userID, id primitive.ObjectID, status string) error {
	res, err := s.db.Col(database.ColContacts).UpdateOne(ctx,
		bson.M{"_id": id, "user_id": userID},
		bson.M{"$set": bson.M{"status": status, "updated_at": time.Now()}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrContactNotFound
	}
	return nil
}

// UnsubscribeByToken is the public one-click unsubscribe. Idempotent — returns
// the contact's email for the confirmation page (empty if the token is unknown).
func (s *ContactService) UnsubscribeByToken(ctx context.Context, token string) string {
	if strings.TrimSpace(token) == "" {
		return ""
	}
	var c models.Contact
	if err := s.db.Col(database.ColContacts).FindOne(ctx, bson.M{"unsub_token": token}).Decode(&c); err != nil {
		return ""
	}
	now := time.Now()
	_, _ = s.db.Col(database.ColContacts).UpdateOne(ctx,
		bson.M{"_id": c.ID},
		bson.M{"$set": bson.M{"status": models.ContactUnsubscribed, "unsub_at": now, "updated_at": now}})
	return c.Email
}

// regexEscape quotes regex metacharacters in a user search term.
func regexEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`.*+?()|[]{}^$\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
