package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Draft is a Gmail-style auto-saved compose in progress. Recipients are stored
// as the raw comma-separated strings the user typed (not parsed Addresses) so a
// half-finished "to" line round-trips exactly when the window is reopened.
type Draft struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID      primitive.ObjectID `bson:"user_id" json:"user_id"`
	AccountID   primitive.ObjectID `bson:"account_id" json:"account_id"`
	To          string             `bson:"to" json:"to"`
	Cc          string             `bson:"cc" json:"cc"`
	Bcc         string             `bson:"bcc" json:"bcc"`
	Subject     string             `bson:"subject" json:"subject"`
	HTML        string             `bson:"html" json:"html"`
	SignatureID string             `bson:"signature_id,omitempty" json:"signature_id,omitempty"`
	InReplyTo   string             `bson:"in_reply_to,omitempty" json:"in_reply_to,omitempty"`
	References  []string           `bson:"references,omitempty" json:"references,omitempty"`
	CreatedAt   time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time          `bson:"updated_at" json:"updated_at"`
}

// DraftRequest is the create/update body. AccountID is required so a draft is
// always tied to the mailbox it will be sent from.
type DraftRequest struct {
	AccountID   string   `json:"account_id" validate:"required"`
	To          string   `json:"to"`
	Cc          string   `json:"cc"`
	Bcc         string   `json:"bcc"`
	Subject     string   `json:"subject"`
	HTML        string   `json:"html"`
	SignatureID string   `json:"signature_id"`
	InReplyTo   string   `json:"in_reply_to"`
	References  []string `json:"references"`
}
