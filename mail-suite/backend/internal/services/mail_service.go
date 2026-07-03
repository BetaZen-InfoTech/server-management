package services

import (
	"context"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/config"
	"github.com/betazeninfotech/mail-suite/internal/database"
	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type MailService struct {
	db       *database.DB
	accounts *AccountService
	sigs     *SignatureService
	cfg      *config.Config
}

func NewMailService(db *database.DB, accounts *AccountService, sigs *SignatureService, cfg *config.Config) *MailService {
	return &MailService{db: db, accounts: accounts, sigs: sigs, cfg: cfg}
}

func (s *MailService) Folders(ctx context.Context, userID, accountID primitive.ObjectID) ([]models.Folder, error) {
	a, err := s.accounts.Get(ctx, userID, accountID)
	if err != nil {
		return nil, err
	}
	return ListFolders(a)
}

func (s *MailService) Headers(ctx context.Context, userID, accountID primitive.ObjectID, folder string, page, limit int) ([]models.MessageHeader, int, error) {
	a, err := s.accounts.Get(ctx, userID, accountID)
	if err != nil {
		return nil, 0, err
	}
	return ListHeaders(a, folder, limit, page)
}

func (s *MailService) Message(ctx context.Context, userID, accountID primitive.ObjectID, folder string, uid uint32) (*models.MessageBody, error) {
	a, err := s.accounts.Get(ctx, userID, accountID)
	if err != nil {
		return nil, err
	}
	return FetchMessage(a, folder, uid)
}

func (s *MailService) Flag(ctx context.Context, userID, accountID primitive.ObjectID, folder string, uid uint32, req models.MessageFlagRequest) error {
	a, err := s.accounts.Get(ctx, userID, accountID)
	if err != nil {
		return err
	}
	var addSeen, removeSeen, addStar, removeStar bool
	if req.Unread != nil {
		if *req.Unread {
			removeSeen = true
		} else {
			addSeen = true
		}
	}
	if req.Starred != nil {
		if *req.Starred {
			addStar = true
		} else {
			removeStar = true
		}
	}
	if addSeen || removeSeen || addStar || removeStar {
		if err := SetFlags(a, folder, uid, addSeen, removeSeen, addStar, removeStar); err != nil {
			return err
		}
	}
	if req.Folder != "" && req.Folder != folder {
		return MoveMessage(a, folder, req.Folder, uid)
	}
	return nil
}

// Send builds and delivers an outbound message. The body sent to recipients is
// the "tracked" version — signature applied, plus (per the mailbox's effective
// tracking settings) an open pixel and/or click-through link rewriting. A clean
// "untracked" copy is archived to the Sent folder and a SentMessage record is
// written for the tracking dashboard. Archiving/persistence are best-effort:
// once SMTP has accepted the mail, their failures never surface as a send error.
func (s *MailService) Send(ctx context.Context, userID, accountID primitive.ObjectID, req *models.SendRequest) error {
	a, err := s.accounts.Get(ctx, userID, accountID)
	if err != nil {
		return err
	}

	sigHTML := s.resolveSignature(ctx, userID, req.SignatureID)
	baseHTML := applySignature(req.HTML, sigHTML) // signature applied, no tracking
	textBody := req.Text
	if textBody == "" {
		textBody = htmlToText(baseHTML)
	}

	tr := a.EffectiveTracking()
	trackID := newToken(16)
	messageID := buildMessageID(a.Address)
	baseURL := s.cfg.PublicURL

	outHTML := baseHTML
	if tr.Click {
		outHTML = rewriteLinks(outHTML, baseURL, trackID, s.cfg.JWTSecret)
	}
	if tr.Open {
		outHTML = injectOpenPixel(outHTML, baseURL, trackID)
	}

	outBuf, err := buildMIME(a, req, outHTML, textBody, messageID)
	if err != nil {
		return err
	}
	rcpts := flattenAddrs(req.To, req.Cc, req.Bcc)
	if err := smtpSend(a, rcpts, outBuf); err != nil {
		return err
	}

	s.archiveSent(ctx, userID, a, req, baseHTML, textBody, messageID, trackID, tr)
	return nil
}

// archiveSent files a clean copy into the Sent folder and persists the tracking
// record. Both legs are best-effort and logged, never fatal to the send.
func (s *MailService) archiveSent(ctx context.Context, userID primitive.ObjectID, a *models.MailAccount, req *models.SendRequest, baseHTML, textBody, messageID, trackID string, tr models.TrackingSettings) {
	if buf, err := buildMIME(a, req, baseHTML, textBody, messageID); err == nil {
		if err := AppendToSent(a, buf); err != nil {
			log.Warn().Err(err).Str("account", a.Address).Msg("append to Sent folder failed")
		}
	} else {
		log.Warn().Err(err).Msg("build Sent copy failed")
	}

	rec := models.SentMessage{
		UserID:        userID,
		AccountID:     a.ID,
		TrackID:       trackID,
		MessageID:     messageID,
		Subject:       req.Subject,
		To:            req.To,
		Cc:            req.Cc,
		Bcc:           req.Bcc,
		Snippet:       clip(htmlToText(baseHTML), 180),
		TrackDelivery: tr.Delivery,
		TrackOpen:     tr.Open,
		TrackClick:    tr.Click,
		Status:        "sent",
		SentAt:        time.Now(),
	}
	if _, err := s.db.Col(database.ColSent).InsertOne(ctx, rec); err != nil {
		log.Warn().Err(err).Msg("persist sent record failed")
	}
}

// resolveSignature returns the HTML of the explicitly-chosen signature, else the
// user's default signature, else "".
func (s *MailService) resolveSignature(ctx context.Context, userID primitive.ObjectID, signatureID string) string {
	if signatureID != "" {
		if sigOID, err := primitive.ObjectIDFromHex(signatureID); err == nil {
			var sig models.Signature
			if err := s.db.Col(database.ColSignatures).FindOne(ctx, bson.M{"_id": sigOID, "user_id": userID}).Decode(&sig); err == nil {
				return sig.HTML
			}
		}
		return ""
	}
	var sig models.Signature
	if err := s.db.Col(database.ColSignatures).FindOne(ctx, bson.M{"user_id": userID, "is_default": true}).Decode(&sig); err == nil {
		return sig.HTML
	}
	return ""
}
