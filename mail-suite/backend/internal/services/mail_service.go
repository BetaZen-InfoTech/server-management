package services

import (
	"context"
	"errors"

	"github.com/betazeninfotech/mail-suite/internal/database"
	"github.com/betazeninfotech/mail-suite/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type MailService struct {
	db       *database.DB
	accounts *AccountService
	sigs     *SignatureService
}

func NewMailService(db *database.DB, accounts *AccountService, sigs *SignatureService) *MailService {
	return &MailService{db: db, accounts: accounts, sigs: sigs}
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

func (s *MailService) Send(ctx context.Context, userID, accountID primitive.ObjectID, req *models.SendRequest) error {
	a, err := s.accounts.Get(ctx, userID, accountID)
	if err != nil {
		return err
	}
	var sigHTML string
	if req.SignatureID != "" {
		sigOID, err := primitive.ObjectIDFromHex(req.SignatureID)
		if err == nil {
			var sig models.Signature
			if err := s.db.Col(database.ColSignatures).FindOne(ctx, bson.M{"_id": sigOID, "user_id": userID}).Decode(&sig); err == nil {
				sigHTML = sig.HTML
			}
		}
	} else {
		// default signature for the user, if any
		var sig models.Signature
		if err := s.db.Col(database.ColSignatures).FindOne(ctx, bson.M{"user_id": userID, "is_default": true}).Decode(&sig); err == nil {
			sigHTML = sig.HTML
		} else if !errors.Is(err, mongo.ErrNoDocuments) {
			// non-fatal
			_ = err
		}
	}
	return SendMail(a, req, sigHTML)
}
