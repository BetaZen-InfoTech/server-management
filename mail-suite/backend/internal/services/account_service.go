package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/config"
	"github.com/betazeninfotech/mail-suite/internal/database"
	"github.com/betazeninfotech/mail-suite/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

var ErrAccountNotFound = errors.New("account not found")

type AccountService struct {
	db  *database.DB
	cfg *config.Config
}

func NewAccountService(db *database.DB, cfg *config.Config) *AccountService {
	return &AccountService{db: db, cfg: cfg}
}

func (s *AccountService) List(ctx context.Context, userID primitive.ObjectID) ([]models.MailAccount, error) {
	cur, err := s.db.Col(database.ColAccounts).Find(ctx, bson.M{"user_id": userID})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []models.MailAccount{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *AccountService) Get(ctx context.Context, userID, id primitive.ObjectID) (*models.MailAccount, error) {
	var a models.MailAccount
	err := s.db.Col(database.ColAccounts).FindOne(ctx, bson.M{"_id": id, "user_id": userID}).Decode(&a)
	if err == mongo.ErrNoDocuments {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *AccountService) Add(ctx context.Context, userID primitive.ObjectID, req models.AddAccountRequest) (*models.MailAccount, error) {
	addr := strings.ToLower(strings.TrimSpace(req.Address))
	a := models.MailAccount{
		UserID: userID,
		DisplayName: req.DisplayName,
		Address: addr,
		Provider: req.Provider,
		Username: req.Username,
		Secret: req.Password,
		Color: req.Color,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if a.Username == "" {
		a.Username = addr
	}
	switch req.Provider {
	case "betazen":
		a.IMAPHost = s.cfg.IMAPHost
		a.IMAPPort = s.cfg.IMAPPort
		a.IMAPSSL = false
		a.SMTPHost = s.cfg.SMTPHost
		a.SMTPPort = s.cfg.SMTPPort
		a.SMTPSSL = false
	default:
		a.IMAPHost = req.IMAPHost
		a.IMAPPort = req.IMAPPort
		a.IMAPSSL = req.IMAPSSL
		a.SMTPHost = req.SMTPHost
		a.SMTPPort = req.SMTPPort
		a.SMTPSSL = req.SMTPSSL
	}

	// First account becomes primary
	cnt, _ := s.db.Col(database.ColAccounts).CountDocuments(ctx, bson.M{"user_id": userID})
	a.IsPrimary = cnt == 0

	res, err := s.db.Col(database.ColAccounts).InsertOne(ctx, a)
	if err != nil {
		return nil, err
	}
	a.ID = res.InsertedID.(primitive.ObjectID)
	return &a, nil
}

// TestConnection validates the given mailbox credentials against the real
// IMAP and SMTP servers without persisting anything. For a "betazen" mailbox
// it uses the panel's configured local mail host; for "imap" it uses the
// caller-supplied host/port/SSL. Returns a descriptive error (prefixed IMAP:/
// SMTP:) on the first failing leg so the UI can tell the operator which side
// is wrong.
func (s *AccountService) TestConnection(ctx context.Context, req models.TestAccountRequest) error {
	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = strings.ToLower(strings.TrimSpace(req.Address))
	}

	imapHost, imapPort, imapSSL := req.IMAPHost, req.IMAPPort, req.IMAPSSL
	smtpHost, smtpPort, smtpSSL := req.SMTPHost, req.SMTPPort, req.SMTPSSL
	if req.Provider == "betazen" {
		imapHost, imapPort, imapSSL = s.cfg.IMAPHost, s.cfg.IMAPPort, false
		smtpHost, smtpPort, smtpSSL = s.cfg.SMTPHost, s.cfg.SMTPPort, false
	}

	if imapHost == "" {
		return fmt.Errorf("IMAP host is required")
	}
	if err := VerifyIMAPLogin(imapHost, imapPort, imapSSL, username, req.Password); err != nil {
		return fmt.Errorf("IMAP: %w", err)
	}
	// SMTP is best-effort-optional: only checked when a host is supplied, so a
	// receive-only mailbox can still be validated.
	if smtpHost != "" {
		if err := VerifySMTPLogin(smtpHost, smtpPort, smtpSSL, username, req.Password); err != nil {
			return fmt.Errorf("SMTP: %w", err)
		}
	}
	return nil
}

func (s *AccountService) Delete(ctx context.Context, userID, id primitive.ObjectID) error {
	res, err := s.db.Col(database.ColAccounts).DeleteOne(ctx, bson.M{"_id": id, "user_id": userID})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrAccountNotFound
	}
	return nil
}

func (s *AccountService) SetPrimary(ctx context.Context, userID, id primitive.ObjectID) error {
	if _, err := s.db.Col(database.ColAccounts).UpdateMany(ctx, bson.M{"user_id": userID}, bson.M{"$set": bson.M{"is_primary": false}}); err != nil {
		return err
	}
	res, err := s.db.Col(database.ColAccounts).UpdateOne(ctx, bson.M{"_id": id, "user_id": userID}, bson.M{"$set": bson.M{"is_primary": true}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrAccountNotFound
	}
	return nil
}
